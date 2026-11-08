package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var runFirstRunFeeSetup = func(ctx context.Context, path string, c Config) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newFeeSetupModel(ctx, path, c)
	result, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if err != nil {
		return false, err
	}
	return result.(feeSetupModel).completed, nil
}

type feeSetupLoginResult struct {
	config Config
	err    error
}

type feeSetupModel struct {
	ctx                          context.Context
	path                         string
	config                       Config
	step, input, source, message string
	choice, width, height        int
	completed, cancelling, quit  bool
	cancel                       context.CancelFunc
	login                        func(context.Context, string) (Config, error)
}

func newFeeSetupModel(ctx context.Context, path string, c Config) feeSetupModel {
	return feeSetupModel{ctx: ctx, path: path, config: c, step: "choice", login: func(ctx context.Context, station string) (Config, error) {
		// shortcut: SSO saves before buy-fee review; split persistence if setup must become all-or-nothing.
		if err := newEVEClient().loginFees(ctx, io.Discard, station, true); err != nil {
			return Config{}, err
		}
		return LoadConfig(path)
	}}
}

func (m feeSetupModel) Init() tea.Cmd { return nil }

func (m feeSetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case feeSetupLoginResult:
		m.cancel()
		if m.quit {
			return m, tea.Quit
		}
		if msg.err != nil || m.cancelling {
			m.step, m.input, m.message = "choice", "", "Login cancelled. Choose another option or try again."
			if msg.err != nil && !m.cancelling {
				m.message = "Login failed: " + msg.err.Error()
			}
			m.cancelling = false
			return m, nil
		}
		m.config.Fees, m.config.EVE = msg.config.Fees, msg.config.EVE
		m.step, m.input = "location", ""
	case tea.KeyMsg:
		key := msg.String()
		if m.step == "login" {
			if key == "esc" || key == "ctrl+c" {
				m.cancelling, m.quit = true, m.quit || key == "ctrl+c"
				m.cancel()
			}
			return m, nil
		}
		if key == "ctrl+c" || (m.step == "choice" && (key == "esc" || key == "q")) {
			return m, tea.Quit
		}
		if key == "esc" {
			m.step, m.input, m.message = "choice", "", ""
			return m, nil
		}
		m.message = ""
		switch m.step {
		case "choice":
			switch key {
			case "up":
				m.choice = (m.choice + 2) % 3
			case "down":
				m.choice = (m.choice + 1) % 3
			case "1", "2", "3":
				m.choice = int(key[0] - '1')
				fallthrough
			case "enter":
				m.input = ""
				m.source = []string{"eve", "manual", "estimates"}[m.choice]
				m.step = []string{"station", "location", "review"}[m.choice]
				if m.source == "estimates" {
					m.config.Fees = DefaultConfig().Fees
				}
			}
		case "location":
			if key == "1" || key == "2" {
				m.config.Fees.BuyLocation = map[string]string{"1": "structure", "2": "npc"}[key]
				m.step = "buy"
				if m.source == "eve" && key == "2" {
					m.config.Fees.BuyBroker = m.config.Fees.Broker
					m.step = "review"
				}
			}
		case "review":
			if key == "enter" {
				fees := m.config.Fees
				fees.Setup = m.source
				if err := updateConfig(m.path, func(c *Config) error {
					if c.Fees.Setup != "pending" {
						return errors.New("fee setup changed in another session; restart ISKrona")
					}
					c.Fees = fees
					return nil
				}); err != nil {
					m.message = "Could not save fees: " + err.Error()
					return m, nil
				}
				m.completed = true
				return m, tea.Quit
			}
		default:
			switch key {
			case "enter":
				if m.step == "station" {
					ctx, cancel := context.WithCancel(m.ctx)
					m.cancel, m.step = cancel, "login"
					station, login := strings.TrimSpace(m.input), m.login
					return m, func() tea.Msg {
						c, err := login(ctx, station)
						return feeSetupLoginResult{c, err}
					}
				}
				rate, err := strconv.ParseFloat(strings.TrimSpace(m.input), 64)
				if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate >= 100 {
					m.message = "Enter a percentage from 0 up to (not including) 100."
					return m, nil
				}
				switch m.step {
				case "buy":
					if m.config.Fees.BuyLocation == "structure" && rate < .5 {
						m.message = "Include the 0.5% SCC surcharge in the total structure rate."
						return m, nil
					}
					m.config.Fees.BuyBroker, m.step = rate/100, "broker"
					if m.source == "eve" {
						m.step = "review"
					}
				case "broker":
					m.config.Fees.Broker, m.step = rate/100, "tax"
				case "tax":
					if m.config.Fees.Broker+rate/100 >= 1 {
						m.message = "Sell broker fee plus sales tax must be below 100%."
						return m, nil
					}
					m.config.Fees.SalesTax, m.step = rate/100, "review"
				}
				m.input = ""
			case "backspace", "delete":
				runes := []rune(m.input)
				m.input = string(runes[:max(0, len(runes)-1)])
			case "ctrl+u":
				m.input = ""
			default:
				if msg.Type == tea.KeyRunes && !msg.Alt && len(m.input)+len(string(msg.Runes)) <= 240 {
					for _, r := range msg.Runes {
						if unicode.IsPrint(r) {
							m.input += string(r)
						}
					}
				}
			}
		}
	}
	return m, nil
}

func (m feeSetupModel) View() string {
	if m.width > 0 && (m.width < marketRelayMinWidth || m.height < marketRelayMinHeight) {
		return "\n  Fee setup needs at least 80 columns and 18 rows. Resize or Ctrl+C to quit.\n"
	}
	styles := newMarketRelayStyles(m.config.Display.Color, m.config.Display.Theme)
	lines := []string{"", styles.title.Render("ISKrona / FEE SETUP"), "", "Set fees before trusting profit estimates.", ""}
	footer := "Enter continue · Esc back · Ctrl+C quit"
	switch m.step {
	case "choice":
		for i, label := range []string{"EVE SSO · use character skills and NPC standings", "Manual fee setup · enter your own percentages", "Continue with estimates · not character-specific"} {
			line := fmt.Sprintf("%d  %s", i+1, label)
			if i == m.choice {
				line = styles.selected.Render("› " + line)
			} else {
				line = "  " + line
			}
			lines = append(lines, line)
		}
		lines = append(lines, "", "SSO uses Jita 4-4 by default; you can choose another NPC station.", "Player structure fees must be entered manually.")
		footer = "↑/↓ select · Enter choose · 1/2/3 choose · Esc/q quit"
	case "station":
		lines = append(lines, "EVE SSO · NPC sell broker fee and sales tax", "Reads Accounting, Broker Relations, and corporation/faction standings.", "Default: "+jita44Name, "", "NPC station name or ID (blank = default):", "> "+m.input+"▏", "", "Structure sell fees? Esc, then choose Manual fee setup.", "Refresh token stays in your OS credential store.")
	case "login":
		lines = append(lines, "Complete EVE SSO in your browser…", "Reading skills and standings for your selected NPC station.")
		if m.cancelling {
			lines = append(lines, "Cancelling login…")
		}
		footer = "Esc cancel login · Ctrl+C cancel and quit"
	case "location":
		lines = append(lines, "Where will you place BUY orders?", "", "1  Player structure · enter its total broker percentage", "2  NPC station")
		if m.source == "eve" {
			lines = append(lines, "", "NPC sell fees saved for: "+m.config.EVE.StationName, "NPC buys use the same station's derived broker fee.")
		}
		footer = "1/2 choose location · Esc back · Ctrl+C quit"
	case "buy", "broker", "tax":
		label := map[string]string{"buy": "Buy-order broker fee", "broker": "Sell-order broker fee", "tax": "Sales tax"}[m.step]
		lines = append(lines, label+" (%)", "Enter the percentage shown in EVE. For example, enter 1.47 for 1.47%.", "", "> "+m.input+"▏")
		if m.step == "buy" && m.config.Fees.BuyLocation == "structure" {
			lines = append(lines, "", "Enter the total structure rate, including the 0.5% SCC surcharge.", "The structure broker component has a 100 ISK minimum per order.")
		}
	case "review":
		label := map[string]string{"eve": "EVE-derived NPC sell fees · buy fees as selected", "manual": "Manually configured fees", "estimates": "ESTIMATED FEES · not verified for your character or station"}[m.source]
		lines = append(lines, styles.heading.Render(label), "", "Buy location: "+m.config.Fees.BuyLocation, fmt.Sprintf("Buy broker: %.3f%% · Sell broker: %.3f%% · Sales tax: %.3f%%", m.config.Fees.BuyBroker*100, m.config.Fees.Broker*100, m.config.Fees.SalesTax*100))
		if m.source == "eve" {
			lines = append(lines, "NPC station: "+m.config.EVE.StationName)
		}
		lines = append(lines, "", "Change fees later in [fees] in config.toml; use iskrona login for SSO.")
		footer = "Enter save and open Market Relay · Esc back · Ctrl+C quit"
	}
	lines = append(lines, "", styles.error.Render(m.message), "", styles.footer.Render(footer))
	width := 76
	if m.width > 0 {
		width = min(m.width-4, 100)
	}
	for i := range lines {
		lines[i] = "  " + relayClip(lines[i], width)
	}
	view := strings.Join(lines, "\n")
	if !m.config.Display.Color {
		return ansi.Strip(view)
	}
	return view
}
