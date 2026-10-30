//go:build !dev

package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMarketRelaySimulationUnavailableInRelease(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	m.devExport = marketRelayExportSimulation(context.Background(), t.TempDir(), nil, nil)
	if m.devExport != nil || marketRelayDevHelp != "" {
		t.Fatal("release includes export simulation")
	}
	before := m.status
	m, cmd := relayKey(m, "f3")
	if cmd != nil || m.status != before || m.snapshot != nil {
		t.Fatal("F3 is active in release")
	}
	m, _ = relayKey(m, "?")
	if strings.Contains(m.View(), "F3") || strings.Contains(m.View(), "simulate") {
		t.Fatal("release help advertises simulation")
	}
}
