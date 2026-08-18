package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	tax       float64
	copyMode  string
}

type marketRelayEvent struct {
	snapshot *marketRelaySnapshot
	orders   []Order
	status   string
	isError  bool
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
	orders        []Order
}

func newMarketRelayModel(c Config, clipboardOK bool, events <-chan tea.Msg) marketRelayModel {
	return newMarketRelayModelWithRuntime(newMarketRelayRuntime(c), func(int) error { return nil }, clipboardOK, events)
}

func newMarketRelayModelWithRuntime(runtime *marketRelayRuntime, saveMaxJumps func(int) error, clipboardOK bool, events <-chan tea.Msg) marketRelayModel {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	return marketRelayModel{
		config:       runtime.configSnapshot(),
		events:       events,
		spinner:      s,
		status:       "Waiting for a new, stable EVE market export…",
		clipboardOK:  clipboardOK,
		runtime:      runtime,
		saveMaxJumps: saveMaxJumps,
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
	go func() {
		err := WatchDirectory(ctx, c.Market.Marketlogs, marketRelayFileHandler(ctx, runtime, clipboardOK, events))
		if err != nil && !errors.Is(err, context.Canceled) {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: err.Error(), isError: true})
		}
	}()

	save := func(maxJumps int) error { return saveMaxJumps(configPath, maxJumps) }
	_, err := tea.NewProgram(newMarketRelayModelWithRuntime(runtime, save, clipboardOK, events), tea.WithAltScreen()).Run()
	return err
}

func marketRelayFileHandler(ctx context.Context, runtime *marketRelayRuntime, clipboardOK bool, events chan<- tea.Msg) func(string) error {
	return func(path string) error {
		sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("Processing %s…", filepath.Base(path))})
		f, err := os.Open(path)
		if err != nil {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%s: %v; retrying", filepath.Base(path), err), isError: true})
			return fmt.Errorf("%w: %v", errRetryFile, err)
		}
		defer f.Close()
		orders, err := ParseMarketCSV(f)
		if err != nil {
			sendMarketRelayEvent(ctx, events, marketRelayEvent{status: fmt.Sprintf("%s: %v", filepath.Base(path), err), isError: true})
			return nil
		}

		c := runtime.configSnapshot()
		snapshot := marketRelaySnapshotForOrders(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), orders, c)

		status := "Market data updated"
		if clipboardOK {
			value, label := snapshot.copyValue(c.Display.CopyMode)
			if value > 0 {
				if err := CopyText(FormatCents(value)); err != nil {
					status = "Clipboard unavailable; market analysis is still ready"
				} else {
					status = fmt.Sprintf("%s copied automatically", label)
				}
			}
		}
		sendMarketRelayEvent(ctx, events, marketRelayEvent{snapshot: snapshot, orders: orders, status: status})
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
		tax:       c.Fees.SalesTax,
		copyMode:  c.Display.CopyMode,
	}
	if snapshot.hasBuy && snapshot.hasSell {
		snapshot.margin = CalculateMargin(snapshot.buyCents, snapshot.sellCents, c.Fees.Broker, c.Fees.SalesTax)
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
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "+":
			m.adjustMaxJumps(1)
			return m, nil
		case "-":
			m.adjustMaxJumps(-1)
			return m, nil
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "y":
			m.copy("sell")
			return m, nil
		case "b":
			m.copy("buy")
			return m, nil
		case "c":
			m.copy(m.config.Display.CopyMode)
			return m, nil
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
			m.orders = msg.orders
			m.snapshot = msg.snapshot
			if msg.orders != nil {
				m.config = m.runtime.configSnapshot()
				m.snapshot = marketRelaySnapshotForOrders(msg.snapshot.item, msg.orders, m.config)
			}
		}
		m.status, m.isError = msg.status, msg.isError
		return m, waitForMarketRelayEvent(m.events)
	}
	return m, nil
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
	styles := newMarketRelayStyles(m.config.Display.Color)
	width := m.width
	if width == 0 {
		width = 120
	}

	header := styles.title.Render("ISKrona / MARKET RELAY") + "  " + styles.muted.Render("LIVE MARKET WATCH")
	meta := styles.muted.Render(fmt.Sprintf("Watching: %s  ·  Max distance: %d jumps", m.config.Market.Marketlogs, m.config.Market.MaxJumps))
	if m.snapshot == nil {
		body := styles.panel.Width(width - 4).Render(styles.heading.Render("CURRENT EXPORT") + "\n\n" + m.spinner.View() + " " + m.status)
		return "\n" + header + "\n" + meta + "\n\n" + body + "\n\n" + m.footer(styles)
	}

	left, right := m.panels(styles, width)
	profit := styles.panel.Width(width - 4).Render(styles.heading.Render("PROFIT EXAMPLES") + "\n" + m.profitTable(styles))
	view := "\n" + header + "\n" + meta + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right) + "\n\n" + profit + "\n\n" + m.footer(styles)
	if m.showHelp {
		view += "\n\n" + styles.panel.Width(width-4).Render(styles.heading.Render("KEYS")+"\n[y] copy sell   [b] copy buy   [c] copy configured target   [+] increase distance   [-] decrease distance   [?] close help   [q] quit")
	}
	return view
}

func (m marketRelayModel) panels(styles marketRelayStyles, width int) (string, string) {
	panelWidth := (width - 6) / 2
	if panelWidth < 36 {
		panelWidth = 36
	}
	margin := "unavailable"
	if m.snapshot.hasBuy && m.snapshot.hasSell {
		margin = fmt.Sprintf("%.2f%%", m.snapshot.margin)
		margin = styles.margin(m.snapshot.margin).Render(margin)
	}
	left := strings.Join([]string{
		styles.heading.Render("CURRENT EXPORT"),
		styles.item.Render(m.snapshot.item),
		styles.muted.Render(fmt.Sprintf("%d eligible orders · latest stable export", m.snapshot.orders)),
	}, "\n")
	autoCopy := "DISABLED"
	if m.clipboardOK {
		autoCopy = strings.ToUpper(m.snapshot.copyMode)
	}
	right := strings.Join([]string{
		styles.heading.Render("RECOMMENDATION"),
		"SELL  " + m.price(m.snapshot.sellCents, m.snapshot.hasSell, styles),
		"BUY   " + m.price(m.snapshot.buyCents, m.snapshot.hasBuy, styles),
		"NET MARGIN  " + margin,
		styles.muted.Render(fmt.Sprintf("Auto-copy: %s", autoCopy)),
	}, "\n")
	return styles.panel.Width(panelWidth).Render(left), styles.panel.Width(panelWidth).Render(right)
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
	previous := int64(0)
	for _, budget := range []int64{100_000_000, 1_000_000_000, 10_000_000_000} {
		units := exampleUnits(m.snapshot.buyCents, budget)
		if units <= previous {
			units = previous * 2
		}
		previous = units
		cost, revenue, net := profitExample(m.snapshot.buyCents, m.snapshot.sellCents, units, m.snapshot.broker, m.snapshot.tax)
		lines = append(lines, fmt.Sprintf("%-12s  %-18s  %-18s  %s ISK", FormatDisplayUnits(units), FormatDisplayCents(cost), FormatDisplayCents(revenue), FormatDisplayCents(net)))
	}
	return strings.Join(lines, "\n")
}

func (m marketRelayModel) footer(styles marketRelayStyles) string {
	controls := styles.footer.Render("[y] sell  [b] buy  [c] copy target  [+/-] distance  [?] help  [q] quit")
	if !m.isError && (m.snapshot == nil || m.status == "Market data updated") {
		return controls
	}
	status := m.status
	if m.isError {
		status = styles.error.Render(status)
	} else {
		status = styles.muted.Render(status)
	}
	return controls + "  " + status
}

type marketRelayStyles struct {
	title, heading, item, price, muted, error, footer, panel lipgloss.Style
	margin                                                   func(float64) lipgloss.Style
}

func newMarketRelayStyles(color bool) marketRelayStyles {
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	styles := marketRelayStyles{
		title:   lipgloss.NewStyle().Bold(true),
		heading: lipgloss.NewStyle().Bold(true),
		item:    lipgloss.NewStyle().Bold(true),
		price:   lipgloss.NewStyle().Bold(true),
		muted:   lipgloss.NewStyle(),
		error:   lipgloss.NewStyle().Bold(true),
		footer:  lipgloss.NewStyle(),
		panel:   panel,
	}
	if !color {
		styles.margin = func(float64) lipgloss.Style { return lipgloss.NewStyle().Bold(true) }
		return styles
	}
	cyan := lipgloss.Color("14")
	styles.title = styles.title.Foreground(cyan)
	styles.heading = styles.heading.Foreground(cyan)
	styles.item = styles.item.Foreground(lipgloss.Color("15"))
	styles.price = styles.price.Foreground(lipgloss.Color("15"))
	styles.muted = styles.muted.Foreground(lipgloss.Color("8"))
	styles.error = styles.error.Foreground(lipgloss.Color("9"))
	styles.footer = styles.footer.Foreground(cyan)
	styles.panel = panel.BorderForeground(lipgloss.Color("6"))
	styles.margin = func(value float64) lipgloss.Style {
		color := lipgloss.Color("10")
		if value < 10 {
			color = lipgloss.Color("9")
		} else if value < 25 {
			color = lipgloss.Color("11")
		}
		return lipgloss.NewStyle().Bold(true).Foreground(color)
	}
	return styles
}
