//go:build dev

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMarketRelayDevExportPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"The Forge-Odysseus-2026.10.30 065638.txt", "iskrona-dev-Odysseus-existing.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths := make(map[string]bool)
	for range 3 {
		path, err := writeMarketRelayDevExport(dir)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(path) != dir || filepath.Ext(path) != ".txt" || paths[path] {
			t.Fatalf("not a unique export in watched directory: %q", path)
		}
		paths[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		checkMarketRelayDevPrices(t, string(data), 0, 0, false)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 5 {
		t.Fatalf("directory entries = %v, error = %v", entries, err)
	}
	for _, entry := range entries {
		if paths[filepath.Join(dir, entry.Name())] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || string(data) != "original" {
			t.Fatalf("existing file changed or staging file left behind: %q, %v", entry.Name(), err)
		}
	}
}

func TestMarketRelayDevPriceVariation(t *testing.T) {
	export, err := varyMarketRelayDevPrices(-500, 500)
	if err != nil {
		t.Fatal(err)
	}
	checkMarketRelayDevPrices(t, export, -500, 500, true)
}

func TestMarketRelayDevPriceVariationChangesMargin(t *testing.T) {
	var margins [2]float64
	for i, offsets := range [][2]int{{-500, 500}, {500, -500}} {
		export, err := varyMarketRelayDevPrices(offsets[0], offsets[1])
		if err != nil {
			t.Fatal(err)
		}
		orders, err := ParseMarketCSV(strings.NewReader(export))
		if err != nil {
			t.Fatal(err)
		}
		summary := AnalyzeMarket(orders, 0)
		if summary.BestBuy == nil || summary.BestSell == nil {
			t.Fatal("expected eligible buy and sell orders")
		}
		margins[i] = CalculateMargin(summary.BuyCents, summary.SellCents, 0, 0, 0)
	}
	if margins[0] <= margins[1] {
		t.Fatalf("expected lower buy and higher sell offsets to increase margin: %v", margins)
	}
}

func checkMarketRelayDevPrices(t *testing.T, export string, buyOffset, sellOffset int, exact bool) {
	t.Helper()
	original := strings.Split(marketRelayDevExport, "\n")
	result := strings.Split(export, "\n")
	if len(original) != len(result) || original[0] != result[0] {
		t.Fatal("export rows or header changed")
	}
	seenBuy, seenSell := false, false
	for i := 1; i < len(original); i++ {
		if original[i] == "" && result[i] == "" {
			continue
		}
		before, after := strings.Split(original[i], ","), strings.Split(result[i], ",")
		if len(before) != len(after) || strings.Join(before[1:], ",") != strings.Join(after[1:], ",") {
			t.Fatalf("row %d: non-price columns changed", i)
		}
		oldPrice, err := parseCents(before[0])
		if err != nil {
			t.Fatal(err)
		}
		price, err := parseCents(after[0])
		if err != nil || price <= 0 || price < oldPrice*9500/10000 || price > oldPrice*10500/10000 || len(strings.Split(after[0], ".")) != 2 || len(strings.Split(after[0], ".")[1]) != 2 {
			t.Fatalf("row %d: invalid or out-of-range price %q", i, after[0])
		}
		offset := sellOffset
		if before[7] == "True" {
			seenBuy = true
			offset = buyOffset
		} else {
			seenSell = true
		}
		if exact && price != oldPrice*int64(10000+offset)/10000 {
			t.Fatalf("row %d: price %d does not match offset %d", i, price, offset)
		}
	}
	if !seenBuy || !seenSell {
		t.Fatal("fixture must contain buy and sell orders")
	}
}

func TestMarketRelaySimulationWaitsForBaselineAndUsesWatcher(t *testing.T) {
	c := relayTestConfig()
	c.Market.Marketlogs = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, stopped := make(chan struct{}), make(chan struct{})
	events := make(chan tea.Msg, 4)
	m := newMarketRelayModel(c, false, events)
	m.devExport = marketRelayExportSimulation(ctx, c.Market.Marketlogs, ready, stopped)
	m, cmd := relayKey(m, "f3")
	if cmd == nil {
		t.Fatal("F3 did not schedule simulation")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		t.Fatalf("simulation completed before watcher baseline: %#v", msg)
	case <-time.After(20 * time.Millisecond):
	}
	ticks := make(chan time.Time)
	scans := make(chan int, 3)
	done := make(chan error, 1)
	go func() {
		defer close(stopped)
		done <- watchDirectory(ctx, c.Market.Marketlogs, marketRelayFileHandler(ctx, m.runtime, false, events), func(dir string) (map[string]fileSignature, error) {
			files, err := scanDirectory(dir)
			scans <- len(files)
			return files, err
		}, ticks, func() { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("watcher error = %v", err)
		}
	}()
	if count := <-scans; count != 0 {
		t.Fatalf("baseline contains %d files; immediate F3 was lost", count)
	}
	select {
	case msg := <-result:
		simulated := msg.(marketRelaySimulationResult)
		if simulated.err != nil || filepath.Dir(simulated.path) != c.Market.Marketlogs {
			t.Fatalf("simulation result = %#v", simulated)
		}
		updated, _ := m.Update(msg)
		m = updated.(marketRelayModel)
		if m.snapshot != nil || m.isError || !strings.Contains(m.toast, "Offline export created:") {
			t.Fatal("simulation bypassed watcher or did not confirm file creation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("simulation did not finish after baseline")
	}
	ticks <- time.Now()
	<-scans
	select {
	case event := <-events:
		t.Fatalf("file processed before two stable scans: %#v", event)
	default:
	}
	ticks <- time.Now()
	<-scans
	for range 2 {
		select {
		case event := <-events:
			updated, _ := m.Update(event)
			m = updated.(marketRelayModel)
		case <-time.After(2 * time.Second):
			t.Fatal("watcher did not publish processing and analysis")
		}
	}
	if m.isError || m.snapshot == nil || len(m.orders) != 52 || m.snapshot.orders != 33 || m.snapshot.buyCents <= 0 || m.snapshot.sellCents <= 0 || len(m.recent) != 1 {
		t.Fatalf("real export did not reach normal analysis: snapshot=%#v, rows=%d, error=%t", m.snapshot, len(m.orders), m.isError)
	}
}

func TestMarketRelayDevSimulationErrors(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	for _, kind := range []string{"missing directory", "not a directory", "watcher stopped", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopped := make(chan struct{})
			switch kind {
			case "missing directory":
				dir = filepath.Join(dir, "missing")
			case "not a directory":
				dir = filepath.Join(dir, "file")
				if err := os.WriteFile(dir, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "watcher stopped":
				close(stopped)
			case "cancelled":
				cancel()
			}
			result := marketRelayExportSimulation(ctx, dir, ready, stopped)().(marketRelaySimulationResult)
			if result.err == nil || result.path != "" {
				t.Fatalf("simulation result = %#v", result)
			}
		})
	}
}

func TestMarketRelaySimulationHelpAndSheets(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	m, _ = relayKey(m, "?")
	m.width, m.height = 80, 18
	if !strings.Contains(m.View(), "F3  simulate offline export") {
		t.Fatalf("dev help missing shortcut:\n%s", m.View())
	}
	for _, sheet := range []string{"?", "i", "k"} {
		m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
		m.devExport = func() tea.Msg { t.Fatal("F3 ran inside a sheet"); return nil }
		m, _ = relayKey(m, sheet)
		_, cmd := relayKey(m, "f3")
		if cmd != nil {
			t.Fatalf("F3 active in %q sheet", sheet)
		}
	}
}
