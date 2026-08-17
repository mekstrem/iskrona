package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if !strings.Contains(withHelp.View(), "KEYS") {
		t.Fatalf("help view = %q", withHelp.View())
	}

	m.clipboardOK = false
	m.copy("buy")
	if !m.isError || m.status != "Clipboard unavailable" {
		t.Fatalf("unavailable clipboard status = %q, error %t", m.status, m.isError)
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
	if err := marketRelayFileHandler(context.Background(), relayTestConfig(), false, events)(path); err != nil {
		t.Fatal(err)
	}
	first := (<-events).(marketRelayEvent)
	second := (<-events).(marketRelayEvent)
	if !strings.Contains(first.status, "Processing PLEX.txt") {
		t.Fatalf("processing event = %#v", first)
	}
	if second.snapshot == nil || second.snapshot.item != "PLEX" || second.snapshot.orders != 2 {
		t.Fatalf("result event = %#v", second)
	}
	if second.status != "Market data updated" {
		t.Fatalf("result status = %q", second.status)
	}
}

func TestMarketRelayFileHandlerRetriesOpenFailure(t *testing.T) {
	events := make(chan tea.Msg, 2)
	path := filepath.Join(t.TempDir(), "missing.txt")
	err := marketRelayFileHandler(context.Background(), relayTestConfig(), false, events)(path)
	if !errors.Is(err, errRetryFile) {
		t.Fatalf("handler error = %v, want retry marker", err)
	}
	<-events // processing
	failure := (<-events).(marketRelayEvent)
	if !failure.isError || !strings.Contains(failure.status, "retrying") {
		t.Fatalf("failure event = %#v", failure)
	}
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
	if err := runMarketRelay(relayTestConfig(), false); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("runMarketRelay error = %v", err)
	}
}
