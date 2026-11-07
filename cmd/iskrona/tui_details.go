package main

import (
	"fmt"
)

func (m marketRelayModel) details(styles marketRelayStyles) []string {
	lines := []string{styles.heading.Render("DETAILS  ·  Esc / q close"),
		fmt.Sprintf("FILTER: max distance %d jumps (not a route)", m.config.Market.MaxJumps)}
	if m.snapshot == nil {
		lines = append(lines, fmt.Sprintf("Buy broker: %.2f%%  ·  Sell broker: %.2f%%  ·  Sales tax: %.2f%%", m.config.Fees.BuyBroker*100, m.config.Fees.Broker*100, m.config.Fees.SalesTax*100))
		if m.config.Fees.BuyLocation == "structure" && m.config.Fees.BuyBroker < .005 {
			lines = append(lines, "Structure: 0.5% SCC + 100 ISK minimum despite configured rate.")
		}
		return append(lines, "Waiting for the first export; no recommendations yet.")
	}
	s := m.snapshot
	lines = append(lines, fmt.Sprintf("Buy broker: %.2f%%  ·  Sell broker: %.2f%%  ·  Sales tax: %.2f%%", s.buyBroker*100, s.broker*100, s.tax*100))
	if s.buyLocation == "structure" && s.buyBroker < .005 {
		lines = append(lines, "Structure: 0.5% SCC + 100 ISK minimum despite configured rate.")
	}
	if !s.hasBuy || !s.hasSell {
		return append(lines, "Trade costs need both an eligible buy and sell order.")
	}
	units := int64(1)
	cost, revenue, net, ok := profitExample(s.buyCents, s.sellCents, units, s.buyBroker, s.broker, s.tax, s.buyLocation)
	if !ok {
		return append(lines, "Trade cost example exceeds the supported amount.")
	}
	buyValue, sellValue := s.buyCents*units, s.sellCents*units
	return append(lines,
		styles.heading.Render("TRADE COSTS  ·  "+FormatDisplayUnits(units)+" unit  ·  ISK"),
		fmt.Sprintf("%-24s %s", "Purchase value", FormatDisplayCents(buyValue)),
		fmt.Sprintf("%-24s %s", "Buy broker fee", FormatDisplayCents(cost-buyValue)),
		fmt.Sprintf("%-24s %s", "Total buy cost", FormatDisplayCents(cost)),
		fmt.Sprintf("%-24s %s", "Sale value", FormatDisplayCents(sellValue)),
		fmt.Sprintf("%-24s %s", "Sell broker + sales tax", FormatDisplayCents(sellValue-revenue)),
		fmt.Sprintf("%-24s %s", "Net proceeds", FormatDisplayCents(revenue)),
		fmt.Sprintf("%-24s %s", "Net profit after fees", styles.margin(float64(net)).Render(FormatDisplayCents(net)+" ISK")),
		styles.muted.Render("Example only; available volume and fills are not checked."))
}
