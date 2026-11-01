package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestMarketRelayUpdateCheckRunsAsStartupCommand(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	calls := 0
	m.checkUpdate = func() string {
		calls++
		return "1.2.3"
	}
	cmd := m.Init()
	if calls != 0 {
		t.Fatal("Init performed the update check synchronously")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 3 || calls != 0 {
		t.Fatalf("startup did not batch the spinner, watcher, and update check: %T, calls=%d", batch, calls)
	}
	msg := batch[2]()
	if got, ok := msg.(marketRelayUpdateAvailable); !ok || got != "1.2.3" || calls != 1 {
		t.Fatalf("update result = %#v, calls=%d", msg, calls)
	}
	updated, next := m.Update(msg)
	if updated.(marketRelayModel).updateVersion != "1.2.3" || next != nil {
		t.Fatal("update result was lost or scheduled another check")
	}
}

func TestMarketRelayUpdateNoticePreservesState(t *testing.T) {
	for _, version := range []string{"", "1.2.3"} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.snapshot = relayTestSnapshot()
		m.status, m.toast, m.lastError, m.isError = "watcher failed", "watcher failed", "watcher failed", true
		before := m.snapshot
		updated, cmd := m.Update(marketRelayUpdateAvailable(version))
		m = updated.(marketRelayModel)
		if cmd != nil || m.snapshot != before || m.status != "watcher failed" || m.toast != m.status || m.lastError != m.status || !m.isError {
			t.Fatalf("update result altered market/error state: %#v", m)
		}
		view := m.View()
		if strings.Contains(view, "Update available:") != (version != "") {
			t.Fatalf("update notice for %q:\n%s", version, view)
		}
		if !strings.Contains(view, "watcher failed") {
			t.Fatal("update notice hid watcher error")
		}
	}
}

func TestMarketRelayUpdateNoticeFitsTerminal(t *testing.T) {
	const releasesURL = "https://github.com/mekstrem/iskrona/releases"
	for _, size := range [][2]int{{80, 18}, {100, 22}, {120, 30}, {180, 24}} {
		for _, version := range []string{"1.2.3", strings.Repeat("1234567890", 10)} {
			for _, mode := range []string{"waiting", "dashboard", "recent", "help", "details", "palette"} {
				m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
				m.width, m.height, m.updateVersion = size[0], size[1], version
				if mode != "waiting" {
					m.snapshot = relayTestSnapshot()
				}
				m.showRecent = mode == "recent"
				m.showHelp, m.showDetails, m.showPalette = mode == "help", mode == "details", mode == "palette"
				view := m.View()
				lines := strings.Split(view, "\n")
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) != size[1] || strings.Contains(view, "\x1b[") {
					t.Fatalf("update notice broke %v %s bounds or monochrome:\n%s", size, mode, view)
				}
				if !strings.Contains(lines[size[1]-4], "Update available:") || !strings.Contains(lines[size[1]-4], releasesURL) || !strings.Contains(view, "Watching:") || !strings.Contains(lines[size[1]-1], "quit") {
					t.Fatalf("notice, full URL, path, or controls missing at %v %s:\n%s", size, mode, view)
				}
				if version == "1.2.3" && !strings.Contains(view, "Update available: v1.2.3") {
					t.Fatal("release version was clipped")
				}
				if mode == "dashboard" && (!strings.Contains(view, "SELL PRICE") || !strings.Contains(view, "BUY PRICE")) {
					t.Fatal("update notice hid market recommendations")
				}
			}
		}
	}
}
