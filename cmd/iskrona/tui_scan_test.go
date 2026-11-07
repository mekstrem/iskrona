package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMarketRelayScanFailureAndRecovery(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	m.snapshot = relayTestSnapshot()
	before := m.snapshot
	const failure = "scan marketlogs directory: permission denied; retrying"
	updated, cmd := m.Update(marketRelayEvent{status: failure, scanStatus: true})
	m = updated.(marketRelayModel)
	if cmd == nil || m.scanError != failure || !strings.Contains(m.View(), failure) || m.snapshot != before {
		t.Fatalf("scan failure was not visible or changed market data: %#v", m)
	}
	updated, _ = m.Update(marketRelayEvent{status: "Market data updated"})
	m = updated.(marketRelayModel)
	if !strings.Contains(m.View(), failure) {
		t.Fatal("market update hid an ongoing scan failure")
	}
	updated, cmd = m.Update(marketRelayEvent{scanStatus: true})
	m = updated.(marketRelayModel)
	if cmd == nil || m.scanError != "" || m.lastError != "" || m.snapshot != before || strings.Contains(m.View(), failure) {
		t.Fatalf("scan recovery left a stale warning or changed market data: %#v", m)
	}
}

func TestMarketRelayScanRecoveryPreservesUnrelatedErrors(t *testing.T) {
	for _, timing := range []string{"before failure", "during failure"} {
		t.Run(timing, func(t *testing.T) {
			m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
			const unrelated = "Clipboard unavailable"
			messages := []marketRelayEvent{
				{status: unrelated, isError: true},
				{status: "scan failed; retrying", scanStatus: true},
			}
			if timing == "during failure" {
				messages[0], messages[1] = messages[1], messages[0]
			}
			for _, msg := range messages {
				updated, _ := m.Update(msg)
				m = updated.(marketRelayModel)
			}
			id := m.toastID
			updated, _ := m.Update(marketRelayEvent{scanStatus: true})
			m = updated.(marketRelayModel)
			if m.scanError != "" || m.status != unrelated || m.toast != unrelated || m.lastError != unrelated || !m.isError || m.toastID != id || !strings.Contains(m.View(), unrelated) {
				t.Fatalf("recovery cleared or masked an unrelated error: %#v", m)
			}
			updated, _ = m.Update(marketRelayToastExpired(id))
			m = updated.(marketRelayModel)
			if !strings.Contains(m.View(), "Last error: "+unrelated) {
				t.Fatal("recovery lost unrelated error history after toast expiry")
			}
		})
	}
}
