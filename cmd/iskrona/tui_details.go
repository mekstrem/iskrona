package main

import (
	"fmt"
)

func (m marketRelayModel) details(styles marketRelayStyles) []string {
	lines := []string{styles.heading.Render("DETAILS  ·  Esc / q close"),
		fmt.Sprintf("FILTER: max distance %d jumps (not a route)", m.config.Market.MaxJumps)}
	buyBroker, broker, tax := m.config.Fees.BuyBroker, m.config.Fees.Broker, m.config.Fees.SalesTax
	if m.snapshot != nil {
		buyBroker, broker, tax = m.snapshot.buyBroker, m.snapshot.broker, m.snapshot.tax
	}
	var gap []string
	if m.height >= 26 {
		gap = []string{""}
	}
	lines = append(lines, fmt.Sprintf("Buy broker: %.2f%%  ·  Sell broker: %.2f%%  ·  Sales tax: %.2f%%", buyBroker*100, broker*100, tax*100))
	lines = append(lines, gap...)
	quantity := FormatDisplayUnits(m.detailsUnits)
	if m.detailsTyping {
		quantity += "▏"
	}
	lines = append(lines, styles.heading.Render("TRADE COSTS  ·  Quantity: "+quantity+"  ·  ISK"))
	if m.snapshot == nil {
		return append(lines, "Waiting for the first export; no recommendations yet.")
	}
	s := m.snapshot
	if !s.hasBuy || !s.hasSell {
		return append(lines, "Trade costs need both an eligible buy and sell order.")
	}
	units := m.detailsUnits
	cost, revenue, net, ok := profitExample(s.buyCents, s.sellCents, units, s.buyBroker, s.broker, s.tax)
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
