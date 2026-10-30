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
		{"profit", 10_000, 20_000, .005, .02, .01, 10_000, []int64{100_000_000, 500_000, 100_500_000, 200_000_000, 6_000_000, 194_000_000, 93_500_000}},
		{"loss", 10_000, 10_000, .02, .02, .01, 10_000, []int64{100_000_000, 2_000_000, 102_000_000, 100_000_000, 3_000_000, 97_000_000, -5_000_000}},
		{"no fees", 10_000, 20_000, 0, 0, 0, 10_000, []int64{100_000_000, 0, 100_000_000, 200_000_000, 0, 200_000_000, 100_000_000}},
		{"rounded fees", 200_000_001, 300_000_001, .02, .02, .01, 1, []int64{200_000_001, 4_000_000, 204_000_001, 300_000_001, 9_000_000, 291_000_001, 87_000_000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
			m.snapshot = &marketRelaySnapshot{buyCents: tc.buy, sellCents: tc.sell, hasBuy: true, hasSell: true, buyBroker: tc.buyBroker, broker: tc.broker, tax: tc.tax}
			lines := m.details(newMarketRelayStyles(false, "default"))
			if !strings.Contains(lines[3], FormatDisplayUnits(tc.units)+" units") {
				t.Fatalf("quantity = %q", lines[3])
			}
			for i, label := range []string{"Purchase value", "Buy broker fee", "Total buy cost", "Sale value", "Sell broker + sales tax", "Net proceeds", "Net profit after fees"} {
				want := fmt.Sprintf("%-24s %s", label, FormatDisplayCents(tc.values[i]))
				if i == 6 {
					want += " ISK"
				}
				if lines[i+4] != want {
					t.Fatalf("%s = %q, want %q", label, lines[i+4], want)
				}
			}
		})
	}
}

func TestMarketRelayDetailsCostsRemainVisibleAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 18}, {100, 22}, {120, 30}, {180, 24}} {
		m := newMarketRelayModel(relayTestConfig(), false, make(chan tea.Msg))
		m.width, m.height = size[0], size[1]
		m.snapshot = relayTestSnapshot()
		m.lastError = "clipboard unavailable"
		m, _ = relayKey(m, "i")
		view := m.View()
		for _, want := range []string{"TRADE COSTS", "Total buy cost", "Net proceeds", "Net profit after fees", "volume and fills are not checked", "Last error:", "[Esc/q] close"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%v missing %q:\n%s", size, want, view)
			}
		}
		if strings.Contains(view, "PROFIT EXAMPLES") || lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("%v repeats dashboard or overflows:\n%s", size, view)
		}
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

func TestMarketRelayDetailsDoesNotShowOverflowedCosts(t *testing.T) {
	for _, prices := range [][2]int64{{1, math.MaxInt64}, {math.MaxInt64, math.MaxInt64}} {
		m := newMarketRelayModel(relayTestConfig(), true, make(chan tea.Msg))
		m.snapshot = &marketRelaySnapshot{hasBuy: true, hasSell: true, buyCents: prices[0], sellCents: prices[1], broker: .02, tax: .01}
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
		if _, _, _, ok := profitExample(tc.buy, tc.sell, tc.units, tc.buyBroker, tc.broker, tc.tax); ok {
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
