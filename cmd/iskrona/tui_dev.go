//go:build dev

package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const marketRelayDevHelp = "F3  simulate offline export (dev build)"

//go:embed "testdata/The Forge-Odysseus-2026.10.30 065638.txt"
var marketRelayDevExport string

func marketRelayExportSimulation(ctx context.Context, dir string, ready, stopped <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		// An immediate F3 must not become part of the watcher's ignored baseline.
		select {
		case <-ctx.Done():
			return marketRelaySimulationResult{err: ctx.Err()}
		case <-stopped:
			return marketRelaySimulationResult{err: errors.New("market watcher is not running")}
		case <-ready:
		}
		select {
		case <-ctx.Done():
			return marketRelaySimulationResult{err: ctx.Err()}
		case <-stopped:
			return marketRelaySimulationResult{err: errors.New("market watcher is not running")}
		default:
		}
		path, err := writeMarketRelayDevExport(dir)
		return marketRelaySimulationResult{path: path, err: err}
	}
}

func writeMarketRelayDevExport(dir string) (string, error) {
	export, err := varyMarketRelayDevPrices(rand.IntN(1001)-500, rand.IntN(1001)-500)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "iskrona-dev-Odysseus-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.WriteString(export)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", err
	}
	path := strings.TrimSuffix(f.Name(), ".tmp") + ".txt"
	// A hard link publishes complete bytes atomically and cannot replace an existing file.
	if err := os.Link(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func varyMarketRelayDevPrices(buyOffset, sellOffset int) (string, error) {
	lines := strings.Split(marketRelayDevExport, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] == "" {
			continue
		}
		fields := strings.Split(lines[i], ",")
		price, err := parseCents(fields[0])
		if err != nil {
			return "", err
		}
		offset := sellOffset
		if fields[7] == "True" {
			offset = buyOffset
		}
		price = price * int64(10000+offset) / 10000
		fields[0] = fmt.Sprintf("%d.%02d", price/100, price%100)
		lines[i] = strings.Join(fields, ",")
	}
	return strings.Join(lines, "\n"), nil
}
