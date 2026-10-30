package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	marketRelayMinWidth  = 80
	marketRelayMinHeight = 18
)

// isInteractiveTerminal is replaceable in tests. Bubble Tea needs a real
// terminal because it takes control of stdin and stdout while the dashboard is
// running.
var isInteractiveTerminal = func() bool {
	stdout, err := os.Stdout.Stat()
	if err != nil || stdout.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	stdin, err := os.Stdin.Stat()
	return err == nil && stdin.Mode()&os.ModeCharDevice != 0
}

type marketRelaySnapshot struct {
	item      string
	orders    int
	buyCents  int64
	sellCents int64
	hasBuy    bool
	hasSell   bool
	margin    float64
	broker    float64
	buyBroker float64
	tax       float64
	copyMode  string
	maxJumps  int
}

type marketRelayEvent struct {
	snapshot *marketRelaySnapshot
	orders   []Order
	status   string
	isError  bool
	copied   bool
}

// marketRelayRuntime is shared by the Bubble Tea event loop and the directory
// watcher. The watcher always reads a consistent configuration snapshot before
// analyzing an export.
type marketRelayRuntime struct {
	mu     sync.RWMutex
	config Config
}

func newMarketRelayRuntime(c Config) *marketRelayRuntime {
	return &marketRelayRuntime{config: c}
}

func (r *marketRelayRuntime) configSnapshot() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config
}

func (r *marketRelayRuntime) setMaxJumps(maxJumps int) {
	r.mu.Lock()
	r.config.Market.MaxJumps = maxJumps
	r.mu.Unlock()
}

func (r *marketRelayRuntime) setTheme(theme string) {
	r.mu.Lock()
	r.config.Display.Theme = theme
	r.mu.Unlock()
}

type marketRelayModel struct {
	config        Config
	events        <-chan tea.Msg
	spinner       spinner.Model
	snapshot      *marketRelaySnapshot
	status        string
	isError       bool
	showHelp      bool
	width, height int
	clipboardOK   bool
	copyPrice     func(int64) error
	runtime       *marketRelayRuntime
	saveMaxJumps  func(int) error
	saveTheme     func(string) error
	orders        []Order
	recent        []marketRelaySnapshot
	showRecent    bool
	dense         bool
	showPalette   bool
	showDetails   bool
	commandIndex  int
	toast         string
	toastID       uint64
	lastError     string
	devExport     tea.Cmd
}

type marketRelayToastExpired uint64

type marketRelaySimulationResult struct {
	path string
	err  error
}

var marketRelayCommands = []string{
	"Copy sell price", "Copy buy price", "Copy configured target",
	"Increase max distance", "Decrease max distance", "Toggle compact spacing",
	"Details", "Help", "Quit",
}

func newMarketRelayModel(c Config, clipboardOK bool, events <-chan tea.Msg) marketRelayModel {
	return newMarketRelayModelWithRuntime(newMarketRelayRuntime(c), func(int) error { return nil }, clipboardOK, events)
}

func newMarketRelayModelWithRuntime(runtime *marketRelayRuntime, saveMaxJumps func(int) error, clipboardOK bool, events <-chan tea.Msg) marketRelayModel {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	return marketRelayModel{
		config:       runtime.configSnapshot(),
		events:       events,
		spinner:      s,
		status:       "Waiting for a new, stable EVE market export…",
		clipboardOK:  clipboardOK,
		runtime:      runtime,
		saveMaxJumps: saveMaxJumps,
		saveTheme:    func(string) error { return nil },
		copyPrice: func(value int64) error {
			return CopyText(FormatCents(value))
		},
	}
}

func runMarketRelay(configPath string, c Config, clipboardOK bool) error {
	if !isInteractiveTerminal() {
		return errors.New("tui requires an interactive terminal")
	}

	events := make(chan tea.Msg, 16)
	runtime := newMarketRelayRuntime(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcherReady, watcherStopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherStopped)
		process := marketRelayFileProcessor(ctx, runtime, clipboardOK, events, ParseMarketCSV)
		err := watchDirectoryWithOptions(ctx, c.Market.Marketlogs, func(path string, signature fileSignature) error {
			return process(path, &signature)
		}, func() { close(watcherReady) }, func(err error) {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%v; retrying", err), isError: true})
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: err.Error(), isError: true})
		}
	}()

	save := func(maxJumps int) error { return saveMaxJumps(configPath, maxJumps) }
	model := newMarketRelayModelWithRuntime(runtime, save, clipboardOK, events)
	model.saveTheme = func(theme string) error { return saveTheme(configPath, theme) }
	model.devExport = marketRelayExportSimulation(ctx, c.Market.Marketlogs, watcherReady, watcherStopped)
	_, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

func marketRelayFileHandler(ctx context.Context, runtime *marketRelayRuntime, clipboardOK bool, events chan<- tea.Msg) func(string) error {
	process := marketRelayFileProcessor(ctx, runtime, clipboardOK, events, ParseMarketCSV)
	return func(path string) error { return process(path, nil) }
}

func marketRelayFileProcessor(ctx context.Context, runtime *marketRelayRuntime, clipboardOK bool, events chan<- tea.Msg, parse func(io.Reader) ([]Order, error)) func(string, *fileSignature) error {
	return func(path string, expected *fileSignature) error {
		sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("Processing %s…", filepath.Base(path))})
		f, err := os.Open(path)
		if err != nil {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%s: %v; retrying", filepath.Base(path), err), isError: true})
			return fmt.Errorf("%w: %v", errRetryFile, err)
		}
		defer f.Close()
		check := func() (fileSignature, error) {
			info, err := f.Stat()
			if err != nil {
				return fileSignature{}, err
			}
			pathInfo, err := os.Stat(path)
			if err != nil {
				return fileSignature{}, err
			}
			if !os.SameFile(info, pathInfo) {
				return fileSignature{}, errors.New("export replaced")
			}
			if info.Size() != pathInfo.Size() || !info.ModTime().Equal(pathInfo.ModTime()) {
				return fileSignature{}, errors.New("export changed while checking")
			}
			return fileSignature{size: info.Size(), modTime: info.ModTime()}, nil
		}
		before, err := check()
		if err == nil && expected != nil && before != *expected {
			err = errors.New("export changed since scan")
		}
		if err != nil {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%s: %v; retrying", filepath.Base(path), err), isError: true})
			return fmt.Errorf("%w: %v", errRetryFile, err)
		}
		orders, parseErr := parse(f)
		after, err := check()
		if err == nil && before != after {
			err = errors.New("export changed while reading")
		}
		if err != nil {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%s: %v; retrying", filepath.Base(path), err), isError: true})
			return fmt.Errorf("%w: %v", errRetryFile, err)
		}
		err = parseErr
		if err != nil {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%s: %v", filepath.Base(path), err), isError: true})
			return errConsumedFile
		}

		c := runtime.configSnapshot()
		snapshot := marketRelaySnapshotForOrders(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), orders, c)

		status := "Market data updated"
		copied, copyFailed := false, false
		if clipboardOK {
			value, label := snapshot.copyValue(c.Display.CopyMode)
			if value > 0 {
				if err := CopyText(FormatCents(value)); err != nil {
					status = "Clipboard unavailable; market analysis is still ready"
					copyFailed = true
				} else {
					status = fmt.Sprintf("%s copied automatically", label)
					copied = true
				}
			}
		}
		sendMarketRelayEvent(ctx, events, marketRelayEvent{snapshot: snapshot, orders: orders, status: status, copied: copied, isError: copyFailed})
		return nil
	}
}

func marketRelaySnapshotForOrders(item string, orders []Order, c Config) *marketRelaySnapshot {
	summary := AnalyzeMarket(orders, c.Market.MaxJumps)
	snapshot := &marketRelaySnapshot{
		item:      item,
		orders:    eligibleOrderCount(orders, c.Market.MaxJumps),
		buyCents:  summary.BuyCents,
		sellCents: summary.SellCents,
		hasBuy:    summary.BestBuy != nil,
		hasSell:   summary.BestSell != nil,
		broker:    c.Fees.Broker,
		buyBroker: c.Fees.BuyBroker,
		tax:       c.Fees.SalesTax,
		copyMode:  c.Display.CopyMode,
		maxJumps:  c.Market.MaxJumps,
	}
	if snapshot.hasBuy && snapshot.hasSell {
		snapshot.margin = CalculateMargin(snapshot.buyCents, snapshot.sellCents, c.Fees.BuyBroker, c.Fees.Broker, c.Fees.SalesTax)
	}
	return snapshot
}

func eligibleOrderCount(orders []Order, maxJumps int) int {
	eligible := 0
	for _, order := range orders {
		if order.Jumps <= maxJumps {
			eligible++
		}
	}
	return eligible
}

func sendMarketRelayEvent(ctx context.Context, events chan<- tea.Msg, event marketRelayEvent) {
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

func waitForMarketRelayEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func (m marketRelayModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForMarketRelayEvent(m.events))
}

func (m marketRelayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.showPalette || m.showDetails || m.showHelp {
			if key == "esc" || key == "q" || (key == "?" && m.showHelp) || (key == "i" && m.showDetails) || (key == "k" && m.showPalette) {
				m.showPalette, m.showDetails, m.showHelp = false, false, false
				return m, nil
			}
			if !m.showPalette {
				return m, nil
			}
			switch key {
			case "up":
				m.commandIndex = (m.commandIndex + len(marketRelayCommands) - 1) % len(marketRelayCommands)
			case "down":
				m.commandIndex = (m.commandIndex + 1) % len(marketRelayCommands)
			case "enter":
				m.showPalette = false
				switch m.commandIndex {
				case 0, 1, 2:
					side := []string{"sell", "buy", m.config.Display.CopyMode}[m.commandIndex]
					m.copy(side)
					return m.notify()
				case 3, 4:
					if m.commandIndex == 3 {
						m.adjustMaxJumps(1)
					} else {
						m.adjustMaxJumps(-1)
					}
					return m.notify()
				case 5:
					m.dense = !m.dense
				case 6:
					m.showDetails = true
				case 7:
					m.showHelp = true
				case 8:
					return m, tea.Quit
				}
			}
			return m, nil
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "k":
			m.showPalette = true
		case "f2":
			m.cycleTheme()
			return m.notify()
		case "f3":
			return m, m.devExport
		case "i":
			m.showDetails = true
		case "d":
			m.showRecent = !m.showRecent
		case "+":
			m.adjustMaxJumps(1)
			return m.notify()
		case "-":
			m.adjustMaxJumps(-1)
			return m.notify()
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "y":
			m.copy("sell")
			return m.notify()
		case "b":
			m.copy("buy")
			return m.notify()
		case "c":
			m.copy(m.config.Display.CopyMode)
			return m.notify()
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case marketRelayEvent:
		if msg.snapshot != nil {
			m.recent = append([]marketRelaySnapshot{*msg.snapshot}, m.recent...)
			if len(m.recent) > 5 {
				m.recent = m.recent[:5]
			}
			m.orders = msg.orders
			m.snapshot = msg.snapshot
			if msg.orders != nil {
				m.config = m.runtime.configSnapshot()
				m.snapshot = marketRelaySnapshotForOrders(msg.snapshot.item, msg.orders, m.config)
			}
		}
		m.status, m.isError = msg.status, msg.isError
		if msg.isError || msg.copied {
			_, cmd := m.notify()
			return m, tea.Batch(waitForMarketRelayEvent(m.events), cmd)
		}
		return m, waitForMarketRelayEvent(m.events)
	case marketRelaySimulationResult:
		m.status, m.isError = "Offline export created: "+filepath.Base(msg.path), false
		if msg.err != nil {
			m.status, m.isError = fmt.Sprintf("Could not simulate export: %v", msg.err), true
		}
		return m.notify()
	case marketRelayToastExpired:
		if uint64(msg) == m.toastID {
			m.toast = ""
		}
	}
	return m, nil
}

func (m *marketRelayModel) notify() (tea.Model, tea.Cmd) {
	m.toast, m.toastID = m.status, m.toastID+1
	if m.isError {
		m.lastError = m.status
	}
	id := m.toastID
	return *m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return marketRelayToastExpired(id) })
}

func (m *marketRelayModel) adjustMaxJumps(delta int) {
	current := m.config.Market.MaxJumps
	if delta < 0 && current == 0 {
		m.status, m.isError = "Max distance is already 0 jumps", false
		return
	}
	next := current + delta
	if err := m.saveMaxJumps(next); err != nil {
		m.status, m.isError = fmt.Sprintf("Could not save max distance: %v", err), true
		return
	}
	m.runtime.setMaxJumps(next)
	m.config = m.runtime.configSnapshot()
	if m.snapshot != nil {
		m.snapshot = marketRelaySnapshotForOrders(m.snapshot.item, m.orders, m.config)
	}
	m.status, m.isError = fmt.Sprintf("Max distance set to %d jumps", next), false
}

func (m *marketRelayModel) cycleTheme() {
	next := marketRelayThemes[(max(0, slices.Index(marketRelayThemes, m.config.Display.Theme))+1)%len(marketRelayThemes)]
	if err := m.saveTheme(next); err != nil {
		m.status, m.isError = fmt.Sprintf("Could not save theme: %v", err), true
		return
	}
	m.runtime.setTheme(next)
	m.config = m.runtime.configSnapshot()
	m.status, m.isError = "Theme: "+strings.ToUpper(next[:1])+next[1:], false
}

func (m *marketRelayModel) copy(side string) {
	if m.snapshot == nil {
		m.status, m.isError = "No market recommendation is available yet", true
		return
	}
	if !m.clipboardOK {
		m.status, m.isError = "Clipboard unavailable", true
		return
	}
	value, label := m.snapshot.copyValue(side)
	if value == 0 {
		m.status, m.isError = fmt.Sprintf("No %s recommendation is available", label), true
		return
	}
	if err := m.copyPrice(value); err != nil {
		m.status, m.isError = "Clipboard unavailable", true
		return
	}
	m.status, m.isError = fmt.Sprintf("%s copied", label), false
}

func (s *marketRelaySnapshot) copyValue(side string) (int64, string) {
	if side == "buy" {
		return s.buyCents, "Buy price"
	}
	return s.sellCents, "Sell price"
}

func (m marketRelayModel) View() string {
	if m.width > 0 && (m.width < marketRelayMinWidth || m.height < marketRelayMinHeight) {
		return "\n  Market Relay needs at least 80 columns and 18 rows. Resize the terminal.\n"
	}
	styles := newMarketRelayStyles(m.config.Display.Color, m.config.Display.Theme)
	width, height := m.width, m.height
	if width == 0 {
		width = 120
	}
	if height == 0 {
		height = 30
	}
	roomy := width >= 100 && height >= 22
	contentWidth := min(width-4, 112)
	inset := (width - contentWidth) / 2
	header := styles.title.Render("ISKrona / MARKET RELAY")
	filter := styles.muted.Render(fmt.Sprintf("FILTER ≤ %d jumps", m.config.Market.MaxJumps))
	header += strings.Repeat(" ", max(2, contentWidth-lipgloss.Width(header)-lipgloss.Width(filter))) + filter
	var lines []string
	switch {
	case m.showPalette:
		lines = append(lines, styles.heading.Render("COMMANDS"), styles.muted.Render("↑/↓ select  ·  Enter run  ·  Esc close"), "")
		for i, command := range marketRelayCommands {
			prefix := "  "
			if i == m.commandIndex {
				prefix = "› "
				lines = append(lines, styles.selected.Width(min(contentWidth, 72)-4).Render(prefix+command))
				continue
			}
			lines = append(lines, prefix+command)
		}
	case m.showHelp:
		lines = []string{styles.heading.Render("KEYS  ·  Esc / q close"),
			"y  copy sell     b  copy buy     c  copy configured target",
			"+  increase distance     -  decrease distance",
			"d  recent exports     i  details     k  commands",
			"F2  next theme: Default / Caldari / Gallente / Minmatar / Amarr",
			"Compact spacing: toggle in commands; keeps prices and profit examples",
			"?  help     q  quit (outside sheets)     Ctrl+C  always quit"}
		if marketRelayDevHelp != "" {
			lines = append(lines, marketRelayDevHelp)
		}
	case m.showDetails:
		lines = m.details(styles)
	default:
		lines = m.dashboard(styles, contentWidth, roomy)
	}
	overlay := m.showPalette || m.showHelp || m.showDetails
	if roomy && overlay && !m.showPalette {
		lines = append([]string{lines[0], ""}, lines[1:]...)
	}
	view := []string{header, ""}
	if roomy {
		view = append([]string{""}, view...)
	}
	if overlay {
		sheetWidth := min(contentWidth, 72)
		for i := range lines {
			lines[i] = relayClip(lines[i], sheetWidth-4)
		}
		padding := 0
		if roomy {
			padding = 1
		}
		sheet := styles.surface.Width(sheetWidth).Padding(padding, 2).Render(strings.Join(lines, "\n"))
		lines = strings.Split(sheet, "\n")
		for i := range lines {
			lines[i] = strings.Repeat(" ", (contentWidth-sheetWidth)/2) + lines[i]
		}
		for range max(0, (height-4-len(view)-len(lines))/2) {
			view = append(view, "")
		}
	}
	view = append(view, lines...)
	status := m.toast
	if status == "" {
		status = m.autoCopyStatus() + "  ·  i details"
		if strings.HasPrefix(m.status, "Processing ") {
			status = m.status
		}
		if m.lastError != "" {
			status = "Last error: " + m.lastError + "  ·  i details"
		}
	}
	status = relayClip(status, contentWidth)
	if m.isError && m.toast != "" {
		status = styles.error.Render(status)
	} else {
		status = styles.muted.Render(status)
	}
	// Keep status and controls at the bottom, independently of dashboard density.
	view = view[:min(len(view), height-4)]
	for len(view) < height-4 {
		view = append(view, "")
	}
	watching := "Watching: " + m.config.Market.Marketlogs
	view = append(view, styles.muted.Render(strings.Repeat("─", contentWidth)), styles.muted.Render(relayClip(watching, contentWidth)), status, m.footer(styles))
	for i := range view {
		view[i] = strings.Repeat(" ", inset) + relayClip(view[i], contentWidth)
	}
	rendered := strings.Join(view, "\n")
	if !m.config.Display.Color {
		return ansi.Strip(rendered)
	}
	return rendered
}

func relayClip(s string, width int) string {
	return ansi.Truncate(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s), width, "…")
}

func (m marketRelayModel) dashboard(styles marketRelayStyles, width int, roomy bool) []string {
	if m.snapshot == nil {
		spinner := m.spinner
		spinner.Style = styles.title.Bold(false)
		spin := spinner.View()
		if !m.config.Display.Color {
			spin = ansi.Strip(spin)
		}
		return []string{styles.muted.Render("MARKET WATCH"), "", spin + " Waiting for a new, stable EVE market export…", "", "Export market orders from EVE to begin."}
	}
	s := m.snapshot
	margin := "unavailable"
	if s.hasBuy && s.hasSell {
		margin = styles.margin(s.margin).Render(fmt.Sprintf("%.2f%%", s.margin))
	}
	lines := []string{styles.muted.Render("CURRENT EXPORT"), styles.item.Render(relayClip(s.item, width))}
	if !m.showRecent || roomy {
		lines = append(lines, styles.muted.Render(fmt.Sprintf("%d eligible orders · latest stable export", s.orders)))
	}
	if roomy && !m.dense {
		lines = append(lines, "")
		if m.height >= 28 {
			lines = append(lines, "")
		}
	}
	if !roomy && !m.dense && !m.showRecent {
		lines = append(lines, "")
	}
	// Wide screens use three distinct zones; terminal fonts cannot provide scale.
	columnWidth := width / 3
	if !roomy {
		columnWidth = width / 2
	}
	column := lipgloss.NewStyle().Width(columnWidth)
	labels := column.Render(styles.heading.Render("SELL PRICE  [y]")) + column.Render(styles.heading.Render("BUY PRICE  [b]"))
	values := column.Render(m.price(s.sellCents, s.hasSell, styles)) + column.Render(m.price(s.buyCents, s.hasBuy, styles))
	if roomy {
		labels += styles.heading.Render("NET MARGIN")
		values += margin
	}
	lines = append(lines, labels)
	if roomy && !m.dense {
		lines = append(lines, "")
	}
	lines = append(lines, values)
	if !roomy {
		if !m.dense && !m.showRecent {
			lines = append(lines, "")
		}
		lines = append(lines, "NET MARGIN  "+margin)
	}
	if m.showRecent {
		if roomy {
			lines = append(lines, "")
		}
		lines = append(lines, styles.heading.Render("RECENT EXPORTS  ·  captured summaries, newest first"))
		if len(m.recent) == 0 {
			lines = append(lines, "No exports received in this session.")
		}
		for _, recent := range m.recent {
			summary := fmt.Sprintf("S %s  B %s  ≤%dj", m.price(recent.sellCents, recent.hasSell, styles), m.price(recent.buyCents, recent.hasBuy, styles), recent.maxJumps)
			itemWidth := max(4, width-2-lipgloss.Width(summary))
			lines = append(lines, relayClip(recent.item, itemWidth)+"  "+summary)
		}
	} else if roomy {
		lines = append(lines, "")
		if !m.dense && m.height >= 28 {
			lines = append(lines, "")
		}
		lines = append(lines, styles.heading.Render("PROFIT EXAMPLES"), styles.muted.Render(fmt.Sprintf("Buy broker %.2f%% · Sell broker %.2f%% · Tax %.2f%%", s.buyBroker*100, s.broker*100, s.tax*100)), "")
		lines = append(lines, strings.Split(m.profitTable(styles), "\n")...)
	} else {
		lines = append(lines, "", styles.muted.Render("i  profit examples & fees  ·  d  recent exports"))
	}
	return lines
}

func (m marketRelayModel) autoCopyStatus() string {
	if m.clipboardOK {
		return "Auto-copy: " + strings.ToUpper(m.config.Display.CopyMode) + " · clipboard available"
	}
	return "Auto-copy: DISABLED · clipboard unavailable"
}

func (m marketRelayModel) price(value int64, available bool, styles marketRelayStyles) string {
	if !available {
		return styles.muted.Render("unavailable")
	}
	return styles.price.Render(FormatDisplayCents(value) + " ISK")
}

func (m marketRelayModel) profitTable(styles marketRelayStyles) string {
	if !m.snapshot.hasBuy || !m.snapshot.hasSell {
		return styles.muted.Render("Profit examples need both an eligible buy and sell order.")
	}
	lines := []string{fmt.Sprintf("%-12s  %-18s  %-18s  %s", "Units", "Cost", "Revenue", "Net profit")}
	compact := m.width > 0 && m.width < 110
	if compact {
		lines = []string{"Units  ·  Net profit after fees (ISK)"}
	}
	previous := int64(0)
	for _, budget := range []int64{100_000_000, 1_000_000_000, 10_000_000_000} {
		units := exampleUnits(m.snapshot.buyCents, budget)
		if units <= 0 {
			lines = append(lines, styles.muted.Render("Profit example exceeds the supported amount."))
			continue
		}
		if units <= previous {
			if previous > math.MaxInt64/2 {
				lines = append(lines, styles.muted.Render("Profit example exceeds the supported amount."))
				continue
			}
			units = previous * 2
		}
		previous = units
		cost, revenue, net, ok := profitExample(m.snapshot.buyCents, m.snapshot.sellCents, units, m.snapshot.buyBroker, m.snapshot.broker, m.snapshot.tax)
		if !ok {
			lines = append(lines, styles.muted.Render("Profit example exceeds the supported amount."))
			continue
		}
		netProfit := styles.margin(float64(net)).Render(FormatDisplayCents(net) + " ISK")
		if compact {
			lines = append(lines, fmt.Sprintf("%s  ·  %s", FormatDisplayUnits(units), netProfit))
		} else {
			lines = append(lines, fmt.Sprintf("%-12s  %-18s  %-18s  %s", FormatDisplayUnits(units), FormatDisplayCents(cost), FormatDisplayCents(revenue), netProfit))
		}
	}
	return strings.Join(lines, "\n")
}

func (m marketRelayModel) footer(styles marketRelayStyles) string {
	if m.showPalette || m.showHelp || m.showDetails {
		return styles.footer.Render("[Esc/q] close  [Ctrl+C] quit")
	}
	return styles.footer.Render("[y] sell b buy c target +/- d recent i info k cmd F2 theme ? help q quit")
}

type marketRelayStyles struct {
	title, heading, item, price, muted, error, footer, surface, selected lipgloss.Style
	margin                                                               func(float64) lipgloss.Style
}

func newMarketRelayStyles(color bool, theme string) marketRelayStyles {
	styles := marketRelayStyles{
		title:    lipgloss.NewStyle().Bold(true),
		heading:  lipgloss.NewStyle().Bold(true),
		item:     lipgloss.NewStyle().Bold(true),
		price:    lipgloss.NewStyle().Bold(true),
		muted:    lipgloss.NewStyle(),
		error:    lipgloss.NewStyle().Bold(true),
		footer:   lipgloss.NewStyle(),
		surface:  lipgloss.NewStyle(),
		selected: lipgloss.NewStyle().Bold(true),
	}
	if !color {
		styles.margin = func(float64) lipgloss.Style { return lipgloss.NewStyle() }
		return styles
	}
	accent := lipgloss.AdaptiveColor{Light: "#6D4CBA", Dark: "#A78BFA"}
	heading := lipgloss.AdaptiveColor{Light: "#5C5665", Dark: "#AAA4B0"}
	muted := lipgloss.AdaptiveColor{Light: "#77717E", Dark: "#8A8491"}
	surface := lipgloss.AdaptiveColor{Light: "#F0EDF4", Dark: "#25232B"}
	selected := lipgloss.AdaptiveColor{Light: "#E4DFEC", Dark: "#35313E"}
	switch theme {
	case "caldari":
		accent = lipgloss.AdaptiveColor{Light: "#235E86", Dark: "#85C4E8"}
		heading = lipgloss.AdaptiveColor{Light: "#435D70", Dark: "#B1C6D5"}
		muted = lipgloss.AdaptiveColor{Light: "#5E7180", Dark: "#94AAB9"}
		surface = lipgloss.AdaptiveColor{Light: "#EAF0F5", Dark: "#202A33"}
		selected = lipgloss.AdaptiveColor{Light: "#D6E4EF", Dark: "#2E4050"}
	case "gallente":
		accent = lipgloss.AdaptiveColor{Light: "#246C60", Dark: "#8DD4BD"}
		heading = lipgloss.AdaptiveColor{Light: "#435F56", Dark: "#AFCDBF"}
		muted = lipgloss.AdaptiveColor{Light: "#60756A", Dark: "#93AE9F"}
		surface = lipgloss.AdaptiveColor{Light: "#EAF2EC", Dark: "#202E29"}
		selected = lipgloss.AdaptiveColor{Light: "#D6E7DC", Dark: "#30463C"}
	case "minmatar":
		accent = lipgloss.AdaptiveColor{Light: "#9A482D", Dark: "#EAB08B"}
		heading = lipgloss.AdaptiveColor{Light: "#725344", Dark: "#D2B9A8"}
		muted = lipgloss.AdaptiveColor{Light: "#7D695C", Dark: "#B49C8A"}
		surface = lipgloss.AdaptiveColor{Light: "#F4ECE5", Dark: "#302620"}
		selected = lipgloss.AdaptiveColor{Light: "#EADACC", Dark: "#4A372B"}
	case "amarr":
		accent = lipgloss.AdaptiveColor{Light: "#846117", Dark: "#E3C77E"}
		heading = lipgloss.AdaptiveColor{Light: "#6D6042", Dark: "#CEC2A1"}
		muted = lipgloss.AdaptiveColor{Light: "#7A705A", Dark: "#B1A58A"}
		surface = lipgloss.AdaptiveColor{Light: "#F5F0E3", Dark: "#2D291F"}
		selected = lipgloss.AdaptiveColor{Light: "#EAE0C4", Dark: "#443C27"}
	}
	styles.title = styles.title.Foreground(accent)
	styles.heading = styles.heading.Bold(false).Foreground(heading)
	styles.muted = styles.muted.Foreground(muted)
	styles.footer = styles.footer.Foreground(heading)
	styles.surface = styles.surface.Background(surface)
	styles.selected = styles.selected.Foreground(accent).Background(selected)
	styles.margin = func(value float64) lipgloss.Style {
		style := lipgloss.NewStyle().Bold(true)
		if value > 0 {
			return style.Foreground(lipgloss.AdaptiveColor{Light: "#237A45", Dark: "#80C99B"})
		}
		if value < 0 {
			return style.Foreground(lipgloss.AdaptiveColor{Light: "#B13C47", Dark: "#ED8796"})
		}
		return style
	}
	return styles
}
