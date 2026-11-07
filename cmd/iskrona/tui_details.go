package main

import (
	"fmt"
)

func (m marketRelayModel) details(styles marketRelayStyles) []string {
	lines := []string{styles.heading.Render(fmt.Sprintf("DETAILS  ·  FILTER: max distance %d jumps (not a route)", m.config.Market.MaxJumps))}
	buyBroker, broker, tax := m.config.Fees.BuyBroker, m.config.Fees.Broker, m.config.Fees.SalesTax
	buyLocation := m.config.Fees.BuyLocation
	if m.snapshot != nil {
		buyBroker, broker, tax = m.snapshot.buyBroker, m.snapshot.broker, m.snapshot.tax
		buyLocation = m.snapshot.buyLocation
	}
	var gap []string
	if m.height >= 26 {
		gap = []string{""}
	}
	lines = append(lines, fmt.Sprintf("Buy broker: %.2f%%  ·  Sell broker: %.2f%%  ·  Sales tax: %.2f%%", buyBroker*100, broker*100, tax*100))
	if buyLocation == "structure" && buyBroker < .005 {
		lines = append(lines, "Structure: 0.5% SCC + 100 ISK minimum despite configured rate.")
	}
	lines = append(lines, gap...)
	quantity := FormatDisplayUnits(m.detailsUnits)
	if m.detailsTyping {
		quantity += "▏"
	}
	lines = append(lines, styles.heading.Render("TRADE COSTS · ISK Quantity: ["+quantity+"]  type to change"))
	if m.snapshot == nil {
		return append(lines, "Waiting for the first export; no recommendations yet.")
	}
	s := m.snapshot
	if !s.hasBuy || !s.hasSell {
		return append(lines, "Trade costs need both an eligible buy and sell order.")
	}
	units := m.detailsUnits
	cost, revenue, net, ok := profitExample(s.buyCents, s.sellCents, units, s.buyBroker, s.broker, s.tax, s.buyLocation)
	if !ok {
		return append(lines, "Trade cost example exceeds the supported amount.")
	}
	buyValue, sellValue := s.buyCents*units, s.sellCents*units
	lines = append(lines,
		fmt.Sprintf("%-24s %s", "Purchase value", FormatDisplayCents(buyValue)),
		fmt.Sprintf("%-24s %s", "Buy broker fee", FormatDisplayCents(cost-buyValue)),
		fmt.Sprintf("%-24s %s", "Total buy cost", FormatDisplayCents(cost)))
	lines = append(lines, gap...)
	lines = append(lines,
		fmt.Sprintf("%-24s %s", "Sale value", FormatDisplayCents(sellValue)),
		fmt.Sprintf("%-24s %s", "Sell broker + sales tax", FormatDisplayCents(sellValue-revenue)),
		fmt.Sprintf("%-24s %s", "Net proceeds", FormatDisplayCents(revenue)))
	lines = append(lines, gap...)
	lines = append(lines, fmt.Sprintf("%-24s %s", "Net profit after fees", styles.margin(float64(net)).Render(FormatDisplayCents(net)+" ISK")))
	lines = append(lines, gap...)
	return append(lines, styles.muted.Render("Example only; available volume and fills are not checked."))
}

func (m marketRelayModel) detailsHelp(styles marketRelayStyles) []string {
	row := func(key, action string) string { return fmt.Sprintf("%-18s%s", key, action) }
	return []string{
		styles.title.Render("DETAILS KEYS"),
		styles.muted.Render("Quantity changes update trade totals as you type."),
		row("0-9", "First digit replaces quantity; next digits append."),
		row("Enter", "Finish entry; next digit starts a new quantity."),
		row("Backspace/Delete", "Remove last digit; quantity stays at least 1."),
		row("+/-", "Change by one unit and finish entry."),
		"Whole digits only; no commas or decimals. Minimum quantity: 1.",
		"Quantities above 9,223,372,036,854,775,807 are ignored.",
		"",
		row("? / Esc / q", "Back to Details; keep your entry."),
		row("Esc / q / i", "Close Details when help is closed."),
		row("Ctrl+C", "Quit from any view."),
	}
}
