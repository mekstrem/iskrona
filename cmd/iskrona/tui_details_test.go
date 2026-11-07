package main

import (
	"fmt"
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestMarketRelayDetailsCostBreakdown(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		buy, sell              int64
		buyBroker, broker, tax float64
		units                  int64
		values                 []int64
	}{
		{"profit", 10_000, 20_000, .005, .02, .01, 1, []int64{10_000, 50, 10_050, 20_000, 600, 19_400, 9_350}},
		{"loss", 10_000, 10_000, .02, .02, .01, 2, []int64{20_000, 400, 20_400, 20_000, 600, 19_400, -1_000}},
		{"no fees", 10_000, 20_000, 0, 0, 0, 3, []int64{30_000, 0, 30_000, 60_000, 0, 60_000, 30_000}},
		{"large quantity", 10_000, 20_000, .005, .02, .01, 10_000, []int64{100_000_000, 500_000, 100_500_000, 200_000_000, 6_000_000, 194_000_000, 93_500_000}},
		{"rounded fees", 200_000_001, 300_000_001, .02, .02, .01, 1, []int64{200_000_001, 4_000_000, 204_000_001, 300_000_001, 9_000_000, 291_000_001, 87_000_000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m.snapshot = &marketRelaySnapshot{buyCents: tc.buy, sellCents: tc.sell, hasBuy: true, hasSell: true, buyBroker: tc.buyBroker, broker: tc.broker, tax: tc.tax}
			m.detailsUnits = tc.units
			lines := m.details(newMarketRelayStyles(false, "default"))
			if !strings.Contains(lines[2], "Quantity: ["+FormatDisplayUnits(tc.units)+"]  type to change") {
				t.Fatalf("quantity = %q", lines[2])
			}
			for i, label := range []string{"Purchase value", "Buy broker fee", "Total buy cost", "Sale value", "Sell broker + sales tax", "Net proceeds", "Net profit after fees"} {
				want := fmt.Sprintf("%-24s %s", label, FormatDisplayCents(tc.values[i]))
				if i == 6 {
					want += " ISK"
				}
				if lines[i+3] != want {
					t.Fatalf("%s = %q, want %q", label, lines[i+3], want)
				}
			}
		})
	}
}

func TestDetailsStructureSurchargeNotice(t *testing.T) {
	for _, tc := range []struct {
		location string
		rate     float64
		notice   bool
	}{
		{"structure", 0, true},
		{"structure", .004, true},
		{"structure", .005, false},
		{"npc", 0, false},
	} {
		for _, snapshot := range []bool{false, true} {
			c := relayTestConfig()
			c.Fees.BuyLocation, c.Fees.BuyBroker = tc.location, tc.rate
			m := newMarketRelayModel(c, true, make(chan tea.Msg))
			if snapshot {
				m.snapshot = &marketRelaySnapshot{buyLocation: tc.location, buyBroker: tc.rate}
			}
			text := strings.Join(m.details(newMarketRelayStyles(false, "default")), "\n")
			if got := strings.Contains(text, "0.5% SCC + 100 ISK minimum"); got != tc.notice {
				t.Errorf("location=%s rate=%g snapshot=%v: notice=%v", tc.location, tc.rate, snapshot, got)
			}
		}
	}
}

func TestMarketRelayDetailsOneUnitAtCompactSize(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
	m.width, m.height, m.showDetails = 80, 18, true
	m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: 10_000, sellCents: 20_000, buyBroker: .005, broker: .02, tax: .01}
	view := m.View()
	for _, want := range []string{"TRADE COSTS · ISK Quantity: [1]  type to change", "Purchase value           100.00", "Net profit after fees    93.50 ISK"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact details missing %q:\n%s", want, view)
		}
	}
}

func TestMarketRelayDetailsStructureCostsAndNoticeFit(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {80, 30}, {99, 21}, {100, 22}, {120, 26}, {120, 30}, {180, 18}, {180, 24}} {
		for _, rate := range []float64{0, .004, .005, .02} {
			m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
			m.width, m.height = size[0], size[1]
			m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: 10_000, sellCents: 30_000, buyBroker: rate, broker: .02, tax: .01, buyLocation: "structure"}
			for _, step := range []struct {
				key         string
				units, cost int64
			}{{"i", 1, 20_050}, {"+", 2, 30_100}, {"10000", 10_000, 100_510_000}} {
				if rate == .02 && step.units == 10_000 {
					step.cost = 102_000_000
				}
				m, _ = relayKey(m, step.key)
				view := m.View()
				values := []int64{10_000 * step.units, step.cost - 10_000*step.units, step.cost, 30_000 * step.units, 900 * step.units, 29_100 * step.units, 29_100*step.units - step.cost}
				for i, label := range []string{"Purchase value", "Buy broker fee", "Total buy cost", "Sale value", "Sell broker + sales tax", "Net proceeds", "Net profit after fees"} {
					want := fmt.Sprintf("%-24s %s", label, FormatDisplayCents(values[i]))
					if i == 6 {
						want += " ISK"
					}
					if !strings.Contains(view, want) {
						t.Fatalf("%v rate %g units %d missing %q:\n%s", size, rate, step.units, want, view)
					}
				}
				for _, want := range []string{"FILTER: max distance 0 jumps (not a route)", "Quantity: [" + FormatDisplayUnits(step.units), "type to change", "Example only; available volume and fills are not checked.", "[+/-] qty", "[?] keys", "[Esc/q] close", "[Ctrl+C] quit"} {
					if !strings.Contains(view, want) {
						t.Fatalf("%v rate %g missing %q:\n%s", size, rate, want, view)
					}
				}
				if got := strings.Contains(view, "Structure: 0.5% SCC + 100 ISK minimum despite configured rate."); got != (rate < .005) {
					t.Fatalf("%v rate %g: surcharge notice visible=%t:\n%s", size, rate, got, view)
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) != size[1] {
					t.Fatalf("%v structure details overflow:\n%s", size, view)
				}
			}
		}
	}
}

func TestMarketRelayDetailsCostsRemainVisibleAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {80, 30}, {99, 21}, {100, 22}, {120, 26}, {120, 30}, {180, 18}, {180, 24}} {
		m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
		m.width, m.height = size[0], size[1]
		m.snapshot = relayTestSnapshot()
		m.lastError = "clipboard unavailable"
		m, _ = relayKey(m, "i")
		view := m.View()
		for _, want := range []string{"TRADE COSTS", "Quantity: [1]  type to change", "Total buy cost", "Net proceeds", "Net profit after fees", "volume and fills are not checked", "Last error:", "[+/-] qty", "[?] keys", "[Esc/q] close", "[Ctrl+C] quit"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%v missing %q:\n%s", size, want, view)
			}
		}
		if strings.Contains(view, "PROFIT EXAMPLES") || lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("%v repeats dashboard or overflows:\n%s", size, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if i := strings.Index(line, "DETAILS"); i >= 0 && i != (size[0]-min(size[0]-4, 112))/2+2 {
				t.Fatalf("%v details did not expand with available width:\n%s", size, view)
			}
		}
	}
}

func TestMarketRelayDetailsQuantityKeys(t *testing.T) {
	for _, open := range []string{"shortcut", "palette"} {
		t.Run(open, func(t *testing.T) {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m.snapshot = &marketRelaySnapshot{buyCents: 10_000, sellCents: 20_000, hasBuy: true, hasSell: true, buyBroker: .005, broker: .02, tax: .01}
			m.width, m.height = 80, 18
			saves, copies := 0, 0
			m.saveMaxJumps = func(int) error { saves++; return nil }
			m.copyPrice = func(int64) error { copies++; return nil }
			before := m.snapshot
			if open == "palette" {
				m, _ = relayKey(m, "k")
				m.commandIndex = 6
				m, _ = relayKey(m, "enter")
			} else {
				m, _ = relayKey(m, "i")
			}
			if !m.showDetails || m.detailsUnits != 1 {
				t.Fatalf("details did not start at one: %#v", m)
			}
			for _, step := range []struct {
				key   string
				units int64
			}{{"-", 1}, {"+", 2}, {"+", 3}, {"-", 2}, {"-", 1}, {"-", 1}} {
				var cmd tea.Cmd
				m, cmd = relayKey(m, step.key)
				if m.detailsUnits != step.units || cmd != nil || m.config.Market.MaxJumps != 0 || m.runtime.configSnapshot().Market.MaxJumps != 0 || saves != 0 || copies != 0 || m.snapshot != before {
					t.Fatalf("%q changed distance or failed quantity %d: %#v, saves=%d, copies=%d", step.key, step.units, m, saves, copies)
				}
				view := m.View()
				for _, row := range []string{
					fmt.Sprintf("Quantity: [%d]  type to change", step.units),
					fmt.Sprintf("%-24s %s", "Total buy cost", FormatDisplayCents(10_050*step.units)),
					fmt.Sprintf("%-24s %s", "Sell broker + sales tax", FormatDisplayCents(600*step.units)),
					fmt.Sprintf("%-24s %s ISK", "Net profit after fees", FormatDisplayCents(9_350*step.units)),
				} {
					if !strings.Contains(view, row) {
						t.Fatalf("quantity %d missing %q:\n%s", step.units, row, view)
					}
				}
			}
			m.detailsUnits = math.MaxInt64
			m, _ = relayKey(m, "+")
			if m.detailsUnits != math.MaxInt64 || !strings.Contains(m.View(), "exceeds the supported amount") {
				t.Fatal("quantity overflow was not contained")
			}
			m, _ = relayKey(m, "esc")
			for _, hint := range []string{"+/- jumps", "i info", "F2 theme", "? help", "q quit"} {
				if !strings.Contains(m.View(), hint) {
					t.Fatalf("dashboard does not show %q", hint)
				}
			}
			m, _ = relayKey(m, "+")
			if m.config.Market.MaxJumps != 1 || saves != 1 || m.detailsUnits != math.MaxInt64 {
				t.Fatal("dashboard +/- no longer adjusts distance independently")
			}
		})
	}
}

func TestMarketRelayDetailsMissingSide(t *testing.T) {
	for _, sides := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.snapshot = &marketRelaySnapshot{hasBuy: sides[0], hasSell: sides[1]}
		m.width, m.height, m.showDetails = 80, 18, true
		view := m.View()
		if !strings.Contains(view, "need both an eligible buy and sell order") || strings.Contains(view, "Total buy cost") {
			t.Fatalf("missing side produces costs:\n%s", view)
		}
	}
}

func TestMarketRelayDetailsDirectQuantityEntry(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m.snapshot = &marketRelaySnapshot{buyCents: 10_000, sellCents: 20_000, hasBuy: true, hasSell: true, buyBroker: .005, broker: .02, tax: .01}
	m.width, m.height = 80, 18
	m, _ = relayKey(m, "i")
	for _, units := range []int64{10_000, 100_000, 1_000_000} {
		for _, digit := range fmt.Sprint(units) {
			m, _ = relayKey(m, string(digit))
		}
		if m.detailsUnits != units || !m.detailsTyping {
			t.Fatalf("typed quantity = %d, want %d", m.detailsUnits, units)
		}
		view := m.View()
		for _, row := range []string{
			"Quantity: [" + FormatDisplayUnits(units) + "▏]  type to change",
			fmt.Sprintf("%-24s %s", "Purchase value", FormatDisplayCents(10_000*units)),
			fmt.Sprintf("%-24s %s", "Buy broker fee", FormatDisplayCents(50*units)),
			fmt.Sprintf("%-24s %s", "Total buy cost", FormatDisplayCents(10_050*units)),
			fmt.Sprintf("%-24s %s", "Sale value", FormatDisplayCents(20_000*units)),
			fmt.Sprintf("%-24s %s", "Sell broker + sales tax", FormatDisplayCents(600*units)),
			fmt.Sprintf("%-24s %s", "Net proceeds", FormatDisplayCents(19_400*units)),
			fmt.Sprintf("%-24s %s ISK", "Net profit after fees", FormatDisplayCents(9_350*units)),
			"[+/-] qty", "[?] keys", "[Esc/q] close", "[Ctrl+C] quit",
		} {
			if !strings.Contains(view, row) {
				t.Fatalf("quantity %d missing %q:\n%s", units, row, view)
			}
		}
		if lipgloss.Width(view) > 80 || lipgloss.Height(view) != 18 {
			t.Fatalf("quantity entry overflows compact view:\n%s", view)
		}
		m, _ = relayKey(m, "enter")
		if m.detailsUnits != units || m.detailsTyping || !m.showDetails || strings.Contains(m.View(), "▏") {
			t.Fatal("Enter did not finish entry while keeping Details open")
		}
	}
}

func TestMarketRelayDetailsQuantityEntryValidation(t *testing.T) {
	m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
	m, _ = relayKey(m, "i")
	for _, step := range []struct {
		key    string
		units  int64
		typing bool
	}{
		{"0", 1, false}, {"000", 1, false}, {"5", 5, true}, {"0", 50, true},
		{"backspace", 5, true}, {"delete", 1, false}, {"backspace", 1, false},
		{"9223372036854775807", math.MaxInt64, true}, {"0", math.MaxInt64, true},
		{"enter", math.MaxInt64, false}, {"9223372036854775808", math.MaxInt64, false},
		{"+", math.MaxInt64, false}, {"2", 2, true}, {"-", 1, false}, {"-", 1, false},
		{"8", 8, true}, {"+", 9, false}, {"7", 7, true}, {"enter", 7, false},
		{"-1", 7, false}, {"1.5", 7, false}, {"1,000", 7, false}, {" 10", 7, false},
		{"１２", 7, false}, {"10x", 7, false}, {strings.Repeat("9", 100), 7, false},
	} {
		var cmd tea.Cmd
		m, cmd = relayKey(m, step.key)
		if m.detailsUnits != step.units || m.detailsTyping != step.typing || cmd != nil || !m.showDetails {
			t.Fatalf("%q: quantity=%d typing=%t, want %d/%t", step.key, m.detailsUnits, m.detailsTyping, step.units, step.typing)
		}
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1000000"), Paste: true})
	m = updated.(marketRelayModel)
	if cmd != nil || m.detailsUnits != 1_000_000 || !m.detailsTyping {
		t.Fatal("numeric paste did not set quantity")
	}
}

func TestMarketRelayDetailsQuantityEntryModalIsolation(t *testing.T) {
	for _, close := range []string{"esc", "q", "i", "ctrl+c"} {
		t.Run(close, func(t *testing.T) {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m.snapshot = relayTestSnapshot()
			before := m.snapshot
			saves, copies := 0, 0
			m.saveMaxJumps = func(int) error { saves++; return nil }
			m.saveTheme = func(string) error { saves++; return nil }
			m.copyPrice = func(int64) error { copies++; return nil }
			m, _ = relayKey(m, "i")
			m, _ = relayKey(m, "123")
			for _, key := range []string{"y", "b", "c", "d", "k", "f2", "up", "down"} {
				var cmd tea.Cmd
				m, cmd = relayKey(m, key)
				if cmd != nil || m.detailsUnits != 123 || !m.detailsTyping || !m.showDetails || m.showHelp || m.showPalette || m.showRecent || m.snapshot != before || m.config.Market.MaxJumps != 0 || saves != 0 || copies != 0 {
					t.Fatalf("%q escaped the quantity modal: %#v", key, m)
				}
			}
			m, cmd := relayKey(m, close)
			if close == "ctrl+c" {
				if cmd == nil {
					t.Fatal("Ctrl+C did not quit")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("Ctrl+C returned a non-quit command")
				}
				return
			}
			if cmd != nil || m.showDetails || m.detailsTyping || m.detailsUnits != 123 {
				t.Fatal("close did not finish quantity entry safely")
			}
			m, _ = relayKey(m, "i")
			m, _ = relayKey(m, "4")
			if m.detailsUnits != 4 {
				t.Fatal("reopening Details appended to an old entry")
			}
		})
	}
	for _, mode := range []string{"dashboard", "help", "palette"} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.showHelp, m.showPalette = mode == "help", mode == "palette"
		m, _ = relayKey(m, "1000000")
		if m.detailsUnits != 1 || m.detailsTyping {
			t.Fatalf("numeric entry leaked into %s", mode)
		}
	}
}

func TestMarketRelayDetailsHelpReturnsToQuantityEntry(t *testing.T) {
	for _, typing := range []bool{false, true} {
		for _, close := range []string{"?", "esc", "q", "ctrl+c"} {
			t.Run(fmt.Sprintf("typing=%t/close=%s", typing, close), func(t *testing.T) {
				m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
				m.width, m.height = 80, 18
				m.snapshot = relayTestSnapshot()
				saves, copies := 0, 0
				m.saveMaxJumps = func(int) error { saves++; return nil }
				m.saveTheme = func(string) error { saves++; return nil }
				m.copyPrice = func(int64) error { copies++; return nil }
				m.devExport = func() tea.Msg { t.Fatal("help triggered an export"); return nil }
				m, _ = relayKey(m, "i")
				m, _ = relayKey(m, "123")
				if !typing {
					m, _ = relayKey(m, "enter")
				}
				before, detailsView := m, m.View()
				m, cmd := relayKey(m, "?")
				if cmd != nil || !m.showHelp || !m.showDetails || !strings.Contains(m.View(), "DETAILS KEYS") {
					t.Fatal("Details-specific help did not open")
				}
				for _, key := range []string{"1", "1000", "+", "-", "enter", "backspace", "delete", "y", "b", "c", "d", "k", "i", "f2", "f3", "up", "down"} {
					m, cmd = relayKey(m, key)
					if cmd != nil || !m.showHelp || !m.showDetails || m.detailsUnits != 123 || m.detailsTyping != typing || m.snapshot != before.snapshot || m.config != before.config || m.runtime.configSnapshot() != before.config || m.showPalette || m.showRecent || saves != 0 || copies != 0 {
						t.Fatalf("%q changed state behind Details help: %#v", key, m)
					}
				}
				m, cmd = relayKey(m, close)
				if close == "ctrl+c" {
					if cmd == nil {
						t.Fatal("Ctrl+C did not quit from Details help")
					}
					if _, ok := cmd().(tea.QuitMsg); !ok {
						t.Fatal("Ctrl+C returned a non-quit command")
					}
					return
				}
				if cmd != nil || m.showHelp || !m.showDetails || m.detailsUnits != 123 || m.detailsTyping != typing || m.View() != detailsView {
					t.Fatal("closing help did not restore Details and its entry state")
				}
				m, _ = relayKey(m, "4")
				want := int64(4)
				if typing {
					want = 1234
				}
				if m.detailsUnits != want || !m.detailsTyping {
					t.Fatalf("entry after help = %d, want %d", m.detailsUnits, want)
				}
			})
		}
	}
}

func TestMarketRelayDetailsEditableQuantityAndHelpFit(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {100, 22}, {120, 30}} {
		for _, tc := range []struct {
			units, buy, sell int64
		}{
			{1, math.MaxInt64 / 4, math.MaxInt64 / 2},
			{1_000_000, 10_000, 30_000},
			{math.MaxInt64, 10_000, 30_000},
		} {
			for _, typing := range []bool{false, true} {
				m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
				m.width, m.height, m.showDetails = size[0], size[1], true
				m.detailsUnits, m.detailsTyping = tc.units, typing
				m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: tc.buy, sellCents: tc.sell, buyBroker: .004, broker: .02, tax: .01, buyLocation: "structure"}
				quantity := FormatDisplayUnits(tc.units)
				if typing {
					quantity += "▏"
				}
				view := m.View()
				for _, want := range []string{"Quantity: [" + quantity + "]  type to change", "Structure: 0.5% SCC + 100 ISK minimum despite configured rate.", "[?] keys", "[Esc/q] close", "[Ctrl+C] quit"} {
					if !strings.Contains(view, want) {
						t.Fatalf("%v quantity %d missing %q:\n%s", size, tc.units, want, view)
					}
				}
				if cost, revenue, net, ok := profitExample(tc.buy, tc.sell, tc.units, .004, .02, .01, "structure"); ok {
					for _, want := range []string{FormatDisplayCents(cost), FormatDisplayCents(revenue), FormatDisplayCents(net) + " ISK", "Example only; available volume and fills are not checked."} {
						if !strings.Contains(view, want) {
							t.Fatalf("%v quantity %d clipped costs or disclaimer %q:\n%s", size, tc.units, want, view)
						}
					}
				} else if !strings.Contains(view, "exceeds the supported amount") {
					t.Fatal("overflow warning missing")
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) != size[1] || strings.Contains(view, "\x1b[") {
					t.Fatalf("Details overflowed or used color at %v:\n%s", size, view)
				}
				m, _ = relayKey(m, "?")
				view = m.View()
				for _, want := range []string{"DETAILS KEYS", "totals as you type", "First digit replaces quantity; next digits append.", "Enter", "Finish entry; next digit starts a new quantity.", "Backspace/Delete", "Remove last digit; quantity stays at least 1.", "+/-", "Change by one unit and finish entry.", "no commas or decimals", "9,223,372,036,854,775,807 are ignored.", "Back to Details; keep your entry.", "Close Details when help is closed.", "Quit from any view.", "[?/Esc/q] back to Details", "[Ctrl+C] quit"} {
					if !strings.Contains(view, want) {
						t.Fatalf("%v Details help missing %q:\n%s", size, want, view)
					}
				}
				if lipgloss.Width(view) > size[0] || lipgloss.Height(view) != size[1] || strings.Contains(view, "\x1b[") || strings.Contains(view, "COPY PRICES") || strings.Contains(view, "DISTANCE") {
					t.Fatalf("Details help overflowed, used color, or showed dashboard shortcuts at %v:\n%s", size, view)
				}
			}
		}
	}
}

func TestMarketRelayDetailsDoesNotShowOverflowedCosts(t *testing.T) {
	for _, prices := range [][2]int64{{1, math.MaxInt64}, {math.MaxInt64, math.MaxInt64}} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: prices[0], sellCents: prices[1], broker: .02, tax: .01}
		m.detailsUnits = 2
		m.width, m.height, m.showDetails = 80, 18, true
		view := m.View()
		if !strings.Contains(view, "exceeds the supported amount") || strings.Contains(view, "Total buy cost") {
			t.Fatalf("overflowed costs displayed:\n%s", view)
		}
	}
}

func TestProfitExamplesRejectOverflow(t *testing.T) {
	for _, tc := range []struct {
		buy, sell, units       int64
		buyBroker, broker, tax float64
	}{
		{1, math.MaxInt64, 2, 0, 0, 0},
		{math.MaxInt64, 1, 1, 0, 0, 0},
		{math.MaxInt64 / 2, 1, 1, 2, 0, 0},
		{1, 1, 1, math.NaN(), 0, 0},
	} {
		if _, _, _, ok := profitExample(tc.buy, tc.sell, tc.units, tc.buyBroker, tc.broker, tc.tax, "npc"); ok {
			t.Fatalf("accepted overflow: %+v", tc)
		}
	}
	if got := exampleUnits(math.MaxInt64, 100_000_000); got != 1 {
		t.Fatalf("large price units = %d", got)
	}
	if got := exampleUnits(1, math.MaxInt64); got != 0 {
		t.Fatalf("overflowed rounded units = %d", got)
	}
	for _, width := range []int{80, 120} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.width = width
		m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: 1, sellCents: math.MaxInt64}
		if table := m.profitTable(newMarketRelayStyles(false, "default")); strings.Count(table, "exceeds the supported amount") != 3 {
			t.Fatalf("width %d: overflowed profit table:\n%s", width, table)
		}
	}
}

func TestProfitTableIncludesOneUnit(t *testing.T) {
	assertVisibleRow := func(view, want string) {
		t.Helper()
		for _, line := range strings.Split(view, "\n") {
			if strings.TrimSpace(line) == want {
				return
			}
		}
		t.Fatalf("missing visible row %q:\n%s", want, view)
	}
	for _, width := range []int{100, 120} {
		for _, tc := range []struct {
			buy    int64
			values [2][7]int64
		}{
			{10_000, [2][7]int64{
				{10_000, 50, 10_050, 20_000, 600, 19_400, 9_350},
				{20_000, 100, 20_100, 40_000, 1_200, 38_800, 18_700},
			}},
			{200_000_000, [2][7]int64{
				{200_000_000, 1_000_000, 201_000_000, 400_000_000, 12_000_000, 388_000_000, 187_000_000},
				{400_000_000, 2_000_000, 402_000_000, 800_000_000, 24_000_000, 776_000_000, 374_000_000},
			}},
		} {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m.width, m.height = width, 30
			m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: tc.buy, sellCents: tc.buy * 2, buyBroker: .005, broker: .02, tax: .01}
			one := tc.values[0]
			want := fmt.Sprintf("%-12s  %-18s  %-18s  %s ISK", "1", FormatDisplayCents(one[2]), FormatDisplayCents(one[5]), FormatDisplayCents(one[6]))
			if width < 110 {
				want = "1  ·  " + FormatDisplayCents(one[6]) + " ISK"
			}
			lines := strings.Split(m.profitTable(newMarketRelayStyles(false, "default")), "\n")
			if len(lines) != 4 || lines[1] != want {
				t.Fatalf("width %d buy %d: unexpected profit table: %v", width, tc.buy, lines)
			}
			dashboard := m.View()
			assertVisibleRow(dashboard, want)
			for i, values := range tc.values {
				key := "i"
				if i == 1 {
					key = "+"
				}
				m, _ = relayKey(m, key)
				if !m.showDetails {
					t.Fatal("Details did not stay open")
				}
				view := m.View()
				assertVisibleRow(view, fmt.Sprintf("TRADE COSTS · ISK Quantity: [%d]  type to change", i+1))
				for j, label := range []string{"Purchase value", "Buy broker fee", "Total buy cost", "Sale value", "Sell broker + sales tax", "Net proceeds", "Net profit after fees"} {
					row := fmt.Sprintf("%-24s %s", label, FormatDisplayCents(values[j]))
					if j == 6 {
						row += " ISK"
					}
					assertVisibleRow(view, row)
				}
			}
			m, _ = relayKey(m, "esc")
			if m.showDetails || m.View() != dashboard {
				t.Fatalf("changing Details quantity altered the dashboard:\n%s", m.View())
			}
		}
	}
}
