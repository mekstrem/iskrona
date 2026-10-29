package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func relayTestConfig() Config {
	return Config{
		Market:  MarketConfig{Marketlogs: "C:\\EVE\\Marketlogs", MaxJumps: 0},
		Fees:    FeeConfig{Broker: 0.02, SalesTax: 0.01},
		Display: DisplayConfig{CopyMode: "sell", Color: false},
	}
}

func relayTestSnapshot() *marketRelaySnapshot {
	return &marketRelaySnapshot{
		item: "PLEX", orders: 47, buyCents: 540000100, sellCents: 543210000,
		hasBuy: true, hasSell: true, margin: 0.42, broker: 0.02, tax: 0.01, copyMode: "sell",
	}
}

func TestMarketRelayRendersSnapshotWithoutColor(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.snapshot = relayTestSnapshot()
	m.status = "PLEX processed"
	m.width, m.height = 120, 30

	view := m.View()
	for _, want := range []string{"ISKrona / MARKET RELAY", "PLEX", "SELL PRICE", "BUY PRICE", "PROFIT EXAMPLES", "NET MARGIN", "[y] sell"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("--no-color view includes ANSI escape codes: %q", view)
	}
}

func TestMarketRelayShowsUnavailableAndResizeStates(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.snapshot = &marketRelaySnapshot{item: "Tritanium", orders: 2, hasSell: true, sellCents: 100}
	m.width, m.height = 120, 30
	if view := m.View(); !strings.Contains(view, "unavailable") {
		t.Fatalf("missing-side view = %q", view)
	}
	m.width = 79
	if view := m.View(); !strings.Contains(view, "at least 80 columns") {
		t.Fatalf("narrow view = %q", view)
	}
}

func TestMarketRelayDoesNotRepeatRoutineStatusInFooter(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.width, m.height = 120, 30
	if view := m.View(); strings.Count(view, "Waiting for a new, stable EVE market export") != 1 {
		t.Fatalf("waiting state repeats status:\n%s", view)
	}

	m.snapshot = relayTestSnapshot()
	m.status = "Market data updated"
	if footer := m.footer(newMarketRelayStyles(false, "default")); strings.Contains(footer, m.status) {
		t.Fatalf("routine footer status = %q", footer)
	}
}

func TestMarketRelayKeyActionsAndHelp(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.snapshot = relayTestSnapshot()
	var copied int64
	m.copyPrice = func(value int64) error { copied = value; return nil }

	m.copy("sell")
	if copied != m.snapshot.sellCents || m.isError || m.status != "Sell price copied" {
		t.Fatalf("copy result = value %d, status %q, error %t", copied, m.status, m.isError)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	withHelp := updated.(marketRelayModel)
	if !strings.Contains(withHelp.View(), "KEYS") || !strings.Contains(withHelp.View(), "increase distance") {
		t.Fatalf("help view = %q", withHelp.View())
	}

	waiting := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	waiting.width, waiting.height = 120, 30
	updated, _ = waiting.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	waitingWithHelp := updated.(marketRelayModel)
	if !strings.Contains(waitingWithHelp.View(), "KEYS") {
		t.Fatalf("waiting help view = %q", waitingWithHelp.View())
	}
	updated, _ = waitingWithHelp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	waitingWithoutHelp := updated.(marketRelayModel)
	if strings.Contains(waitingWithoutHelp.View(), "KEYS") {
		t.Fatalf("waiting help remained visible = %q", waitingWithoutHelp.View())
	}

	m.clipboardOK = false
	m.copy("buy")
	if !m.isError || m.status != "Clipboard unavailable" {
		t.Fatalf("unavailable clipboard status = %q, error %t", m.status, m.isError)
	}
}

func TestMarketRelayAdjustsMaxJumpsWithPlusMinus(t *testing.T) {
	c := relayTestConfig()
	runtime := newMarketRelayRuntime(c)
	saved := []int{}
	m := newMarketRelayModelWithRuntime(runtime, func(maxJumps int) error {
		saved = append(saved, maxJumps)
		return nil
	}, true, make(chan tea.Msg))
	orders := []Order{
		{PriceCents: 200, Bid: false, Jumps: 0},
		{PriceCents: 100, Bid: true, Jumps: 0},
		{PriceCents: 50, Bid: false, Jumps: 1},
	}
	initial := marketRelaySnapshotForOrders("Tritanium", orders, c)
	updated, _ := m.Update(marketRelayEvent{snapshot: initial, orders: orders, status: "Market data updated"})
	loaded := updated.(marketRelayModel)
	if loaded.snapshot.orders != 2 || loaded.snapshot.sellCents != 199 {
		t.Fatalf("initial snapshot = %#v", loaded.snapshot)
	}

	updated, _ = loaded.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	increased := updated.(marketRelayModel)
	if increased.config.Market.MaxJumps != 1 || runtime.configSnapshot().Market.MaxJumps != 1 {
		t.Fatalf("max jumps after + = model %d, runtime %d", increased.config.Market.MaxJumps, runtime.configSnapshot().Market.MaxJumps)
	}
	if increased.snapshot.orders != 3 || increased.snapshot.sellCents != 49 || increased.status != "Max distance set to 1 jumps" {
		t.Fatalf("snapshot after + = %#v, status = %q", increased.snapshot, increased.status)
	}

	updated, _ = increased.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	decreased := updated.(marketRelayModel)
	if decreased.config.Market.MaxJumps != 0 || decreased.snapshot.orders != 2 || decreased.snapshot.sellCents != 199 {
		t.Fatalf("snapshot after - = %#v, max jumps = %d", decreased.snapshot, decreased.config.Market.MaxJumps)
	}
	if got, want := saved, []int{1, 0}; !slices.Equal(got, want) {
		t.Fatalf("saved max jumps = %v, want %v", got, want)
	}
}

func TestMarketRelayDoesNotDecreaseMaxJumpsBelowZero(t *testing.T) {
	saves := 0
	m := newMarketRelayModelWithRuntime(newMarketRelayRuntime(relayTestConfig()), func(int) error {
		saves++
		return nil
	}, true, make(chan tea.Msg))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	got := updated.(marketRelayModel)
	if got.config.Market.MaxJumps != 0 || saves != 0 || got.isError || got.status != "Max distance is already 0 jumps" {
		t.Fatalf("model = %#v, saves = %d", got, saves)
	}
}

func TestMarketRelayJumpChangeSaveFailureIsSafe(t *testing.T) {
	runtime := newMarketRelayRuntime(relayTestConfig())
	m := newMarketRelayModelWithRuntime(runtime, func(int) error { return errors.New("disk full") }, true, make(chan tea.Msg))
	m.orders = []Order{{PriceCents: 100, Bid: false, Jumps: 0}}
	m.snapshot = marketRelaySnapshotForOrders("Tritanium", m.orders, m.config)
	before := m.snapshot
	copies := 0
	m.copyPrice = func(int64) error {
		copies++
		return nil
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got := updated.(marketRelayModel)
	if got.config.Market.MaxJumps != 0 || runtime.configSnapshot().Market.MaxJumps != 0 || got.snapshot != before {
		t.Fatalf("save failure changed state: %#v", got)
	}
	if !got.isError || !strings.Contains(got.status, "Could not save max distance") || copies != 0 {
		t.Fatalf("status = %q, error = %t, copies = %d", got.status, got.isError, copies)
	}
}

func TestMarketRelayJumpChangeDoesNotAutoCopy(t *testing.T) {
	m := newMarketRelayModelWithRuntime(newMarketRelayRuntime(relayTestConfig()), func(int) error { return nil }, true, make(chan tea.Msg))
	m.orders = []Order{{PriceCents: 100, Bid: false, Jumps: 0}}
	m.snapshot = marketRelaySnapshotForOrders("Tritanium", m.orders, m.config)
	copies := 0
	m.copyPrice = func(int64) error {
		copies++
		return nil
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got := updated.(marketRelayModel)
	if copies != 0 || strings.Contains(got.status, "copied") {
		t.Fatalf("distance change copied a price: copies = %d, status = %q", copies, got.status)
	}
}

func TestMarketRelayPersistsOnlyMaxJumpsWithPathOverride(t *testing.T) {
	dir := t.TempDir()
	persistedMarketlogs := filepath.Join(dir, "persisted")
	overrideMarketlogs := filepath.Join(dir, "override")
	for _, path := range []string{persistedMarketlogs, overrideMarketlogs} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "config.toml")
	persisted := Config{
		Market:  MarketConfig{Marketlogs: persistedMarketlogs, MaxJumps: 0},
		Fees:    FeeConfig{Broker: 0.025, SalesTax: 0.011},
		Display: DisplayConfig{CopyMode: "buy", Color: false},
		EVE:     EVEConfig{CharacterID: 42, CharacterName: "Capsuleer", StationID: 60003760, StationName: "Jita IV - Moon 4"},
	}
	if err := SaveConfig(path, persisted); err != nil {
		t.Fatal(err)
	}
	effective, err := ResolveConfig(path, CLIOverrides{Path: &overrideMarketlogs})
	if err != nil {
		t.Fatal(err)
	}
	m := newMarketRelayModelWithRuntime(newMarketRelayRuntime(effective), func(maxJumps int) error {
		return saveMaxJumps(path, maxJumps)
	}, true, make(chan tea.Msg))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got := updated.(marketRelayModel)
	if got.config.Market.Marketlogs != overrideMarketlogs || got.config.Market.MaxJumps != 1 {
		t.Fatalf("active config = %#v", got.config)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted.Market.MaxJumps = 1
	if saved != persisted {
		t.Fatalf("persisted config = %#v, want %#v", saved, persisted)
	}
}

func TestMarketRelayCopyReportsMissingSideAndWriteFailure(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.snapshot = &marketRelaySnapshot{hasSell: true, sellCents: 100}
	m.copy("buy")
	if !m.isError || m.status != "No Buy price recommendation is available" {
		t.Fatalf("missing-side status = %q, error %t", m.status, m.isError)
	}

	m.copyPrice = func(int64) error { return errors.New("copy failed") }
	m.copy("sell")
	if !m.isError || m.status != "Clipboard unavailable" {
		t.Fatalf("copy-failure status = %q, error %t", m.status, m.isError)
	}
}

func TestMarketRelayAppliesWatcherError(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.snapshot = relayTestSnapshot()
	updated, _ := m.Update(marketRelayEvent{status: "bad.txt: invalid export", isError: true})
	got := updated.(marketRelayModel)
	if !got.isError || got.status != "bad.txt: invalid export" || got.snapshot == nil {
		t.Fatalf("model = %#v", got)
	}
}

func TestMarketRelayFileHandlerPublishesProcessingAndResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PLEX.txt")
	if err := os.WriteFile(path, []byte("price,bid,jumps,\n5400000.00,True,0,\n5432100.00,False,0,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan tea.Msg, 2)
	if err := marketRelayFileHandler(context.Background(), newMarketRelayRuntime(relayTestConfig()), false, events)(path); err != nil {
		t.Fatal(err)
	}
	first := (<-events).(marketRelayEvent)
	second := (<-events).(marketRelayEvent)
	if !strings.Contains(first.status, "Processing PLEX.txt") {
		t.Fatalf("processing event = %#v", first)
	}
	if second.snapshot == nil || second.snapshot.item != "PLEX" || second.snapshot.orders != 2 || len(second.orders) != 2 {
		t.Fatalf("result event = %#v", second)
	}
	if second.status != "Market data updated" {
		t.Fatalf("result status = %q", second.status)
	}
}

func TestMarketRelayFileHandlerRetriesOpenFailure(t *testing.T) {
	events := make(chan tea.Msg, 2)
	path := filepath.Join(t.TempDir(), "missing.txt")
	err := marketRelayFileHandler(context.Background(), newMarketRelayRuntime(relayTestConfig()), false, events)(path)
	if !errors.Is(err, errRetryFile) {
		t.Fatalf("handler error = %v, want retry marker", err)
	}
	<-events // processing
	failure := (<-events).(marketRelayEvent)
	if !failure.isError || !strings.Contains(failure.status, "retrying") {
		t.Fatalf("failure event = %#v", failure)
	}
}

func TestMarketRelayFileHandlerUsesLatestRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Tritanium.txt")
	if err := os.WriteFile(path, []byte("price,bid,jumps,\n200.00,False,0,\n100.00,True,0,\n50.00,False,1,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := newMarketRelayRuntime(relayTestConfig())
	runtime.setMaxJumps(1)
	events := make(chan tea.Msg, 2)
	if err := marketRelayFileHandler(context.Background(), runtime, false, events)(path); err != nil {
		t.Fatal(err)
	}
	<-events // processing
	result := (<-events).(marketRelayEvent)
	if result.snapshot == nil || result.snapshot.orders != 3 || result.snapshot.sellCents != 4999 {
		t.Fatalf("result = %#v", result)
	}
}

func TestMarketRelayRuntimeConfigSynchronizesConcurrentAccess(t *testing.T) {
	runtime := newMarketRelayRuntime(relayTestConfig())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for maxJumps := 0; maxJumps < 1_000; maxJumps++ {
			runtime.setMaxJumps(maxJumps)
		}
	}()
	for range 1_000 {
		if got := runtime.configSnapshot().Market.MaxJumps; got < 0 {
			t.Fatalf("max jumps = %d, want nonnegative", got)
		}
	}
	<-done
}

func TestMarketRelayCountsOnlyEligibleOrders(t *testing.T) {
	orders := []Order{{Jumps: 0}, {Jumps: 2}, {Jumps: 3}}
	if got := eligibleOrderCount(orders, 2); got != 2 {
		t.Fatalf("eligibleOrderCount = %d, want 2", got)
	}
}

func TestMarketRelayShowsDisabledAutoCopyWhenClipboardUnavailable(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	m.snapshot = relayTestSnapshot()
	m.width, m.height = 120, 30
	view := m.View()
	if !strings.Contains(view, "Auto-copy: DISABLED") {
		t.Fatalf("view does not show disabled auto-copy:\n%s", view)
	}
}

func TestMarketRelayQuitKeyReturnsCommand(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("quit key did not return a quit command")
	}
}

func TestMarketRelayRejectsNonInteractiveTerminal(t *testing.T) {
	original := isInteractiveTerminal
	isInteractiveTerminal = func() bool { return false }
	defer func() { isInteractiveTerminal = original }()
	if err := runMarketRelay("", relayTestConfig(), false); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("runMarketRelay error = %v", err)
	}
}

func relayKey(m marketRelayModel, key string) (marketRelayModel, tea.Cmd) {
	k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		k.Type = tea.KeyEnter
	case "up":
		k.Type = tea.KeyUp
	case "down":
		k.Type = tea.KeyDown
	case "esc":
		k.Type = tea.KeyEsc
	case "ctrl+c":
		k.Type = tea.KeyCtrlC
	case "f2":
		k.Type = tea.KeyF2
	}
	updated, cmd := m.Update(k)
	return updated.(marketRelayModel), cmd
}

func TestMarketRelayPaletteActions(t *testing.T) {
	for index := range marketRelayCommands {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.snapshot = relayTestSnapshot()
		var copied int64
		m.copyPrice = func(value int64) error { copied = value; return nil }
		m, _ = relayKey(m, "k")
		for range index {
			m, _ = relayKey(m, "down")
		}
		if m.commandIndex != index {
			t.Fatalf("selection = %d, want %d", m.commandIndex, index)
		}
		m, cmd := relayKey(m, "enter")
		if m.showPalette {
			t.Fatal("palette remained open")
		}
		switch index {
		case 0, 1, 2:
			want, _ := m.snapshot.copyValue([]string{"sell", "buy", "sell"}[index])
			if copied != want || m.toast == "" || cmd == nil {
				t.Fatalf("copy action: %d %q", copied, m.toast)
			}
		case 3:
			if m.config.Market.MaxJumps != 1 {
				t.Fatal("increase did not run")
			}
		case 4:
			if m.config.Market.MaxJumps != 0 || m.isError {
				t.Fatal("decrease broke zero floor")
			}
		case 5:
			if !m.dense {
				t.Fatal("dense did not toggle")
			}
		case 6:
			if !m.showDetails {
				t.Fatal("details did not open")
			}
		case 7:
			if !m.showHelp {
				t.Fatal("help did not open")
			}
		case 8:
			if cmd == nil {
				t.Fatal("quit command missing")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("not a quit command")
			}
		}
	}
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m, _ = relayKey(m, "k")
	m, _ = relayKey(m, "up")
	if m.commandIndex != len(marketRelayCommands)-1 {
		t.Fatal("up did not wrap")
	}
}

func TestMarketRelaySheetsCloseWithoutQuitting(t *testing.T) {
	for _, open := range []string{"k", "i", "?"} {
		for _, close := range []string{"esc", "q", "ctrl+c"} {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m, _ = relayKey(m, open)
			m, cmd := relayKey(m, close)
			if close == "ctrl+c" {
				if cmd == nil {
					t.Fatal("Ctrl+C did not quit")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("Ctrl+C command is not quit")
				}
			} else if cmd != nil || m.showPalette || m.showDetails || m.showHelp {
				t.Fatalf("%s / %s did not close safely", open, close)
			}
		}
	}
}

func TestMarketRelayDetailsBeforeAndAfterExport(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	m.width, m.height, m.showDetails = 80, 18, true
	for _, want := range []string{"FILTER: max distance", "Broker: 2.00%", "Sales tax: 1.00%", "Auto-copy: DISABLED", "Waiting for the first export"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("waiting details missing %q:\n%s", want, m.View())
		}
	}
	m.snapshot = relayTestSnapshot()
	if view := m.View(); !strings.Contains(view, "Net profit after fees") || strings.Contains(view, "Waiting for the first export") {
		t.Fatalf("populated details:\n%s", view)
	}
}

func TestMarketRelayToastExpiryAndPersistentErrors(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m, cmd := relayKey(m, "+")
	if cmd == nil || m.toast == "" || m.toastID == 0 {
		t.Fatal("distance toast not scheduled")
	}
	oldID := m.toastID
	m, _ = relayKey(m, "y") // no data: a newer error toast
	updated, _ := m.Update(marketRelayToastExpired(oldID))
	m = updated.(marketRelayModel)
	if m.toast == "" {
		t.Fatal("stale timer cleared newer toast")
	}
	updated, _ = m.Update(marketRelayToastExpired(m.toastID))
	m = updated.(marketRelayModel)
	if m.toast != "" || m.lastError == "" || !strings.Contains(m.View(), "Last error:") {
		t.Fatal("expiry lost error history")
	}
	updated, cmd = m.Update(marketRelayEvent{status: "watcher failed", isError: true})
	m = updated.(marketRelayModel)
	if cmd == nil || m.toast != "watcher failed" || m.lastError != "watcher failed" {
		t.Fatal("watcher error not retained and toasted")
	}
}

func TestMarketRelayRecentSnapshotsAndDenseCap(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	for i := range 7 {
		s := relayTestSnapshot()
		s.item = string(rune('A' + i))
		s.maxJumps = i
		updated, _ := m.Update(marketRelayEvent{snapshot: s, status: "Market data updated"})
		m = updated.(marketRelayModel)
		s.item = "mutated"
	}
	if len(m.recent) != 5 || m.recent[0].item != "G" || m.recent[4].item != "C" {
		t.Fatalf("history: %#v", m.recent)
	}
	m, _ = relayKey(m, "+")
	if m.recent[0].maxJumps != 6 {
		t.Fatal("filter rewrote history")
	}
	m.width, m.height = 80, 18
	m, _ = relayKey(m, "d")
	for _, dense := range []bool{false, true} {
		m.dense = dense
		view := m.View()
		if strings.Count(view, "≤") != 6 {
			t.Fatalf("five captured filters not visible (dense %t):\n%s", dense, view)
		}
	}
	m.showRecent = false
	m.dense = false
	m.width, m.height = 120, 30
	regularProfitRow := strings.Count(strings.Split(m.View(), "PROFIT EXAMPLES")[0], "\n")
	m, _ = relayKey(m, "k")
	m.commandIndex = 5
	m, _ = relayKey(m, "enter")
	compactProfitRow := strings.Count(strings.Split(m.View(), "PROFIT EXAMPLES")[0], "\n")
	if !m.dense || compactProfitRow >= regularProfitRow || !strings.Contains(m.View(), "PROFIT EXAMPLES") {
		t.Fatal("dense view did not compact")
	}
	m, _ = relayKey(m, "k")
	m, _ = relayKey(m, "enter")
	if m.dense {
		t.Fatal("dense toggle did not restore")
	}
}

func TestMarketRelayViewBoundsAndMonochrome(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {100, 22}, {120, 30}, {180, 24}} {
		for _, populated := range []bool{false, true} {
			for _, mode := range []string{"normal", "dense", "recent", "help", "details", "palette"} {
				m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
				m.width, m.height = size[0], size[1]
				m.config.Market.Marketlogs = strings.Repeat("long/path/界/", 40)
				m.toast = strings.Repeat("watcher error ", 40)
				m.lastError = m.toast
				if populated {
					m.snapshot = relayTestSnapshot()
					m.snapshot.item = strings.Repeat("Long item 界\n", 40)
					for range 5 {
						m.recent = append(m.recent, *m.snapshot)
					}
				}
				m.dense, m.showRecent = mode == "dense", mode == "recent"
				m.showHelp, m.showDetails, m.showPalette = mode == "help", mode == "details", mode == "palette"
				view := m.View()
				if mode == "normal" && !strings.Contains(strings.Split(view, "\n")[size[1]-1], "q quit") {
					t.Fatalf("quit control clipped at %v:\n%s", size, view)
				}
				if mode == "palette" && (!strings.Contains(view, "Quit") || !strings.Contains(view, "› Copy sell price")) {
					t.Fatalf("palette selection or last action clipped at %v:\n%s", size, view)
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] || strings.Contains(view, "\x1b[") {
					for _, line := range strings.Split(view, "\n") {
						if lipgloss.Width(line) > size[0] {
							t.Logf("overflow %d: %q", lipgloss.Width(line), line)
						}
					}
					t.Fatalf("%v populated=%t %s: %dx%d:\n%s", size, populated, mode, lipgloss.Width(view), lipgloss.Height(view), view)
				}
			}
		}
	}
}

func TestMarketRelayRoomyHierarchy(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {180, 24}} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.snapshot = relayTestSnapshot()
		m.width, m.height = size[0], size[1]
		if m.dense {
			t.Fatal("default must use airy spacing")
		}
		view := m.View()
		lines := strings.Split(view, "\n")
		find := func(text string) int {
			for i, line := range lines {
				if strings.Contains(line, text) {
					return i
				}
			}
			t.Fatalf("missing %q in:\n%s", text, view)
			return -1
		}
		itemRow, labelRow, valueRow := find("PLEX"), find("SELL PRICE"), find(FormatDisplayCents(m.snapshot.sellCents)+" ISK")
		if labelRow-itemRow < 3 || valueRow-labelRow != 2 {
			t.Fatalf("price hierarchy lacks separation:\n%s", view)
		}
		if !strings.Contains(lines[labelRow], "BUY PRICE") || !strings.Contains(lines[labelRow], "NET MARGIN") {
			t.Fatal("three recommendation zones missing")
		}
		if find("PROFIT EXAMPLES") <= valueRow+1 || find("[y] sell") != size[1]-1 {
			t.Fatal("profit section or anchored footer missing")
		}
		for _, line := range lines {
			if strings.ContainsAny(line, "┌┐└┘│") {
				t.Fatal("dashboard must not be a boxed data dump")
			}
			if strings.Contains(line, "─") && lipgloss.Width(strings.TrimSpace(line)) > 112 {
				t.Fatal("divider stretched beyond content width")
			}
		}
		if strings.Count(view, "Auto-copy:") != 1 || strings.Count(view, "Watching:") != 1 {
			t.Fatal("secondary metadata repeated")
		}
		m.dense = true
		if !strings.Contains(m.View(), "PROFIT EXAMPLES") {
			t.Fatal("compact spacing hid profits")
		}
	}
}

func TestMarketRelayRenderedPreviews(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {180, 24}, {80, 18}} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.width, m.height = size[0], size[1]
		m.snapshot = marketRelaySnapshotForOrders("The Forge-Akoman-2026.10.25 191732", []Order{
			{PriceCents: 669700001, Bid: false, Jumps: 0},
			{PriceCents: 515299999, Bid: true, Jumps: 0},
		}, m.config)
		m.status = "Market data updated"
		t.Logf("%dx%d actual View():\n%s", size[0], size[1], m.View())
		if size[0] == 120 {
			m, _ = relayKey(m, "k")
			t.Logf("120x30 commands actual View():\n%s", m.View())
		}
	}
}

func TestMarketRelaySemanticColors(t *testing.T) {
	styles := newMarketRelayStyles(true, "default")
	for _, tc := range []struct {
		value float64
		want  lipgloss.TerminalColor
	}{
		{17.03, lipgloss.AdaptiveColor{Light: "#237A45", Dark: "#80C99B"}},
		{0.001, lipgloss.AdaptiveColor{Light: "#237A45", Dark: "#80C99B"}},
		{-0.001, lipgloss.AdaptiveColor{Light: "#B13C47", Dark: "#ED8796"}},
		{-17.03, lipgloss.AdaptiveColor{Light: "#B13C47", Dark: "#ED8796"}},
		{0, lipgloss.NoColor{}},
	} {
		if got := styles.margin(tc.value).GetForeground(); got != tc.want {
			t.Fatalf("value %g foreground = %#v, want %#v", tc.value, got, tc.want)
		}
		if got := newMarketRelayStyles(false, "default").margin(tc.value).GetForeground(); got != (lipgloss.NoColor{}) {
			t.Fatalf("no-color value %g has foreground %#v", tc.value, got)
		}
	}
}

func TestMarketRelayProfitColorsPreserveLayoutAndNoColor(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	for _, sell := range []int64{20_000, 10_000, 5_000} {
		for _, width := range []int{80, 120} {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m.width, m.height = width, 30
			m.snapshot = &marketRelaySnapshot{item: "PLEX", buyCents: 10_000, sellCents: sell, hasBuy: true, hasSell: true}
			m.snapshot.margin = CalculateMargin(10_000, sell, 0, 0)
			colored := m.profitTable(newMarketRelayStyles(true, "default"))
			plain := m.profitTable(newMarketRelayStyles(false, "default"))
			if ansi.Strip(colored) != plain || strings.Contains(plain, "\x1b[") || lipgloss.Width(colored) != lipgloss.Width(plain) {
				t.Fatalf("profit styling changed layout at width %d:\n%s\n%s", width, colored, plain)
			}
			units := exampleUnits(10_000, 100_000_000)
			_, _, net := profitExample(10_000, sell, units, 0, 0)
			want := newMarketRelayStyles(true, "default").margin(float64(net)).Render(FormatDisplayCents(net) + " ISK")
			row := strings.Split(colored, "\n")[1]
			prefix, found := strings.CutSuffix(row, want)
			if !found || strings.Contains(prefix, "\x1b[") {
				t.Fatalf("net profit not styled independently at width %d: %q", width, row)
			}
			for _, details := range []bool{false, true} {
				m.showDetails = details
				plainView := m.View()
				if strings.Contains(plainView, "\x1b[") {
					t.Fatal("no-color View contains ANSI in a color-capable terminal")
				}
				m.config.Display.Color = true
				coloredView := m.View()
				if ansi.Strip(coloredView) != plainView {
					t.Fatalf("color changed View layout, width %d details %t", width, details)
				}
				if width == 120 || details {
					if !strings.Contains(coloredView, want) {
						t.Fatal("semantic net profit color missing from dashboard/details")
					}
				}
				m.config.Display.Color = false
			}
		}
	}
}

func TestMarketRelayCyclesThemes(t *testing.T) {
	for _, initial := range []string{"", "default"} {
		c := relayTestConfig()
		c.Display.Theme = initial
		m := newMarketRelayModel(c, true, make(chan tea.Msg))
		var saved []string
		m.saveTheme = func(theme string) error { saved = append(saved, theme); return nil }
		for _, want := range []string{"caldari", "gallente", "minmatar", "amarr", "default"} {
			var cmd tea.Cmd
			m, cmd = relayKey(m, "f2")
			if m.config.Display.Theme != want || m.runtime.configSnapshot().Display.Theme != want || m.isError || m.toast == "" || cmd == nil {
				t.Fatalf("cycle to %s: config = %#v, status = %q", want, m.config, m.status)
			}
			// A new export and a distance change must not restore the old theme.
			updated, _ := m.Update(marketRelayEvent{snapshot: relayTestSnapshot(), orders: []Order{}})
			m = updated.(marketRelayModel)
			m, _ = relayKey(m, "+")
			if m.config.Display.Theme != want {
				t.Fatal("runtime update lost theme")
			}
		}
		if !slices.Equal(saved, []string{"caldari", "gallente", "minmatar", "amarr", "default"}) {
			t.Fatalf("saved theme sequence = %v", saved)
		}
	}
}

func TestMarketRelayThemeSaveFailureIsSafe(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.saveTheme = func(string) error { return errors.New("disk full") }
	before := m.config
	m, _ = relayKey(m, "f2")
	if m.config != before || m.runtime.configSnapshot() != before || !m.isError || !strings.Contains(m.toast, "Could not save theme: disk full") || m.lastError != m.toast {
		t.Fatalf("failed save changed state or hid error: %#v", m)
	}
}

func TestMarketRelayThemeShortcutDoesNotConflict(t *testing.T) {
	for _, sheet := range []string{"k", "i", "?"} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m, _ = relayKey(m, sheet)
		before := m.config
		m, _ = relayKey(m, "f2")
		if m.config != before {
			t.Fatalf("F2 changed theme inside %s sheet", sheet)
		}
	}
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	before := m.config
	m, _ = relayKey(m, "t")
	if m.config != before {
		t.Fatal("typing a letter changed theme")
	}
	m, _ = relayKey(m, "?")
	if !strings.Contains(m.View(), "F2  next theme") {
		t.Fatal("theme shortcut missing from help")
	}
}

func TestMarketRelayThemePersistsWithoutOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	persisted := DefaultConfig()
	persisted.Market = MarketConfig{Marketlogs: dir, MaxJumps: 7}
	persisted.EVE = EVEConfig{CharacterID: 42, StationID: 60003760}
	if err := SaveConfig(path, persisted); err != nil {
		t.Fatal(err)
	}
	jumps, copyMode := 2, "buy"
	effective, err := ResolveConfig(path, CLIOverrides{Jumps: &jumps, CopyMode: &copyMode, NoColor: true})
	if err != nil {
		t.Fatal(err)
	}
	m := newMarketRelayModel(effective, true, make(chan tea.Msg))
	m.saveTheme = func(theme string) error { return saveTheme(path, theme) }
	// Preserve changes on disk since startup, not just the startup snapshot.
	persisted.Fees.Broker = 0.04
	if err := SaveConfig(path, persisted); err != nil {
		t.Fatal(err)
	}
	m, _ = relayKey(m, "f2")
	if m.config.Display.Color || m.config.Display.CopyMode != copyMode || m.config.Market.MaxJumps != jumps {
		t.Fatalf("theme change lost active overrides: %#v", m.config)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted.Display.Theme = "caldari"
	if saved != persisted {
		t.Fatalf("theme save changed unrelated settings: %#v, want %#v", saved, persisted)
	}
	restarted := newMarketRelayModel(saved, true, make(chan tea.Msg))
	if restarted.config.Display.Theme != "caldari" {
		t.Fatal("restarted model lost saved theme")
	}
}

func TestMarketRelayThemeStylesAndLayout(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	defaults := newMarketRelayStyles(true, "default")
	if defaults.title.GetForeground() != (lipgloss.AdaptiveColor{Light: "#6D4CBA", Dark: "#A78BFA"}) {
		t.Fatal("default accent changed")
	}
	for _, theme := range marketRelayThemes {
		styles := newMarketRelayStyles(true, theme)
		if theme != "default" && styles.title.GetForeground() == defaults.title.GetForeground() {
			t.Fatalf("%s did not change accent", theme)
		}
		for _, value := range []float64{-1, 0, 1} {
			if styles.margin(value).GetForeground() != defaults.margin(value).GetForeground() {
				t.Fatalf("%s changed semantic color for %g", theme, value)
			}
		}
		for _, size := range [][2]int{{80, 18}, {120, 30}} {
			for _, mode := range []string{"waiting", "dashboard", "help", "details", "palette"} {
				m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
				m.config.Display.Theme = theme
				m.width, m.height = size[0], size[1]
				if mode != "waiting" {
					m.snapshot = relayTestSnapshot()
				}
				m.showHelp, m.showDetails, m.showPalette = mode == "help", mode == "details", mode == "palette"
				plain := m.View()
				m.config.Display.Color = true
				colored := m.View()
				if strings.Contains(plain, "\x1b[") || ansi.Strip(colored) != plain || lipgloss.Width(colored) > size[0] || lipgloss.Height(colored) > size[1] {
					t.Fatalf("%s %s at %v changed layout or violated no-color", theme, mode, size)
				}
				filter := styles.muted.Render("FILTER ≤ 0 jumps")
				if !strings.Contains(colored, filter) {
					t.Fatalf("%s filter does not use themed colors", theme)
				}
				if mode == "dashboard" && (!strings.Contains(colored, "F2 theme") || !strings.Contains(colored, "q quit")) {
					t.Fatalf("shortcut footer clipped at %v: %s", size, plain)
				}
			}
		}
	}
}
