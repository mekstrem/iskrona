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
	for _, want := range []string{"ISKrona / MARKET RELAY", "PLEX", "RECOMMENDATION", "PROFIT EXAMPLES", "NET MARGIN", "[y] sell"} {
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
	if footer := m.footer(newMarketRelayStyles(false)); strings.Contains(footer, m.status) {
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
