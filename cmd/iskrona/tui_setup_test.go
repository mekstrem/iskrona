package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func feeSetupTestModel(t *testing.T) feeSetupModel {
	t.Helper()
	doctorEnvironment(t)
	path := useIsolatedUserConfigDir(t)
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Display.Color = false
	return newFeeSetupModel(context.Background(), path, c)
}

func feeSetupKey(m feeSetupModel, key string) (feeSetupModel, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+u":
		msg = tea.KeyMsg{Type: tea.KeyCtrlU}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	}
	next, cmd := m.Update(msg)
	return next.(feeSetupModel), cmd
}

func TestFeeSetupOnlyNewConfigsArePending(t *testing.T) {
	for _, kind := range []string{"new", "current", "empty", "legacy in place", "legacy tenal", "current tenal"} {
		t.Run(kind, func(t *testing.T) {
			path := useIsolatedUserConfigDir(t)
			data := "[fees]\nbroker = 0.031\nsales_tax = 0.043\n"
			destination := path
			if strings.Contains(kind, "legacy") {
				data = "broker_fee = 0.031\nsales_tax = 0.043\n"
			}
			if kind == "empty" {
				data = ""
			}
			if strings.Contains(kind, "tenal") {
				var err error
				destination, err = legacyConfigPath()
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind != "new" {
				if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				c, err := LoadConfig(path)
				if err != nil {
					t.Fatal(err)
				}
				if pending := c.Fees.Setup == "pending"; pending != (kind == "new") {
					t.Fatalf("load %d: setup = %q", i, c.Fees.Setup)
				}
				if kind != "new" && kind != "empty" && (c.Fees.Broker != .031 || c.Fees.SalesTax != .043) {
					t.Fatalf("existing fees changed: %#v", c.Fees)
				}
			}
		})
	}
}

func TestFeeSetupChoices(t *testing.T) {
	for _, tc := range []struct{ key, source, step string }{{"1", "eve", "station"}, {"2", "manual", "location"}, {"3", "estimates", "review"}} {
		m := feeSetupTestModel(t)
		m, cmd := feeSetupKey(m, tc.key)
		if cmd != nil || m.source != tc.source || m.step != tc.step {
			t.Fatalf("%s: source=%s step=%s command=%v", tc.key, m.source, m.step, cmd)
		}
	}
	m := feeSetupTestModel(t)
	for _, key := range []string{"up", "down", "down", "enter"} {
		m, _ = feeSetupKey(m, key)
	}
	if m.source != "manual" || m.step != "location" {
		t.Fatal("keyboard menu selection failed")
	}
}

func TestFeeSetupManualPersistsOnlyFees(t *testing.T) {
	m := feeSetupTestModel(t)
	// Simulate CLI overrides; setup must never save these.
	m.config.Market.Marketlogs = t.TempDir()
	m.config.Market.MaxJumps = 42
	m.config.Display.Theme = "amarr"
	for _, key := range []string{"2", "1", "1.25", "enter", "2.5", "enter", "3.375", "enter"} {
		m, _ = feeSetupKey(m, key)
	}
	if m.step != "review" || m.completed {
		t.Fatalf("manual did not reach review: %#v", m)
	}
	if err := saveMaxJumps(m.path, 9); err != nil {
		t.Fatal(err)
	}
	if err := saveTheme(m.path, "gallente"); err != nil {
		t.Fatal(err)
	}
	m, cmd := feeSetupKey(m, "enter")
	if !m.completed || cmd == nil {
		t.Fatalf("save failed: %s", m.message)
	}
	c, err := LoadConfig(m.path)
	if err != nil {
		t.Fatal(err)
	}
	want := FeeConfig{Setup: "manual", BuyLocation: "structure", BuyBroker: .0125, Broker: .025, SalesTax: .03375}
	if c.Fees != want || c.Market.MaxJumps != 9 || c.Display.Theme != "gallente" || !c.Display.Color || c.Market.Marketlogs == m.config.Market.Marketlogs {
		t.Fatalf("saved config = %#v", c)
	}
}

func TestFeeSetupEstimatesStayLabelled(t *testing.T) {
	m := feeSetupTestModel(t)
	m, _ = feeSetupKey(m, "3")
	if !strings.Contains(m.View(), "ESTIMATED FEES") {
		t.Fatal("estimates not disclosed before save")
	}
	m, _ = feeSetupKey(m, "enter")
	c, err := LoadConfig(m.path)
	if err != nil || !m.completed || c.Fees.Setup != "estimates" {
		t.Fatalf("save = %#v, %v, completed=%v", c, err, m.completed)
	}
	c.Display.Color = false
	r := newMarketRelayModel(c, false, make(chan tea.Msg))
	r.width, r.height = 80, 18
	for _, snapshot := range []*marketRelaySnapshot{nil, relayTestSnapshot()} {
		r.snapshot = snapshot
		for _, details := range []bool{false, true} {
			r.showDetails = details
			r.toast, r.scanError = "Price copied", "Scan failed; retrying"
			if !strings.Contains(r.View(), "ESTIMATED FEES") {
				t.Fatalf("estimates not labelled with details=%v snapshot=%v:\n%s", details, snapshot != nil, r.View())
			}
		}
	}
}

func TestFeeSetupRejectsInvalidRates(t *testing.T) {
	for _, input := range []string{"", "NaN", "+Inf", "-1", "100", "1,5", "one"} {
		m := feeSetupTestModel(t)
		m.step, m.source, m.input = "tax", "manual", input
		m, _ = feeSetupKey(m, "enter")
		if m.step != "tax" || m.message == "" || m.completed {
			t.Fatalf("accepted invalid percentage %q: %#v", input, m)
		}
	}
	m := feeSetupTestModel(t)
	m.step, m.source, m.input = "tax", "manual", "50"
	m.config.Fees.Broker = .5
	m, _ = feeSetupKey(m, "enter")
	if m.step != "tax" || m.message == "" {
		t.Fatal("accepted combined sell fees >= 100%")
	}
	m.step, m.input = "buy", "0"
	m, _ = feeSetupKey(m, "enter")
	if m.step != "buy" || !strings.Contains(m.message, "0.5% SCC") {
		t.Fatal("structure total omitted the SCC surcharge")
	}
	m.config.Fees.BuyLocation = "npc"
	m, _ = feeSetupKey(m, "enter")
	if m.step != "broker" || m.config.Fees.BuyBroker != 0 {
		t.Fatal("zero NPC broker rate rejected")
	}
}

func TestFeeSetupCancellationKeepsPendingConfig(t *testing.T) {
	for _, key := range []string{"esc", "q", "ctrl+c"} {
		m := feeSetupTestModel(t)
		before, err := os.ReadFile(m.path)
		if err != nil {
			t.Fatal(err)
		}
		m, cmd := feeSetupKey(m, key)
		if cmd == nil || m.completed {
			t.Fatalf("%s did not cancel", key)
		}
		after, err := os.ReadFile(m.path)
		if err != nil || string(before) != string(after) {
			t.Fatal("cancellation changed config")
		}
	}
	m := feeSetupTestModel(t)
	for _, key := range []string{"2", "1", "5", "enter", "6", "enter", "esc"} {
		m, _ = feeSetupKey(m, key)
	}
	if m.step != "choice" || m.completed {
		t.Fatal("Escape did not return to choices")
	}
	c, err := LoadConfig(m.path)
	if err != nil || c.Fees.Setup != "pending" || c.Fees.BuyBroker != .005 {
		t.Fatalf("partial manual fees leaked: %#v, %v", c.Fees, err)
	}
}

func TestFeeSetupSSOChoicesAndFailures(t *testing.T) {
	for _, location := range []string{"1", "2"} {
		m := feeSetupTestModel(t)
		m.login = func(_ context.Context, station string) (Config, error) {
			if station != "60003760" {
				t.Fatalf("station = %q", station)
			}
			c := m.config
			c.Display.Color = true
			c.Fees.Broker, c.Fees.SalesTax = .012, .03375
			c.EVE = EVEConfig{CharacterID: 42, StationID: jita44ID, StationName: jita44Name}
			return c, nil
		}
		for _, key := range []string{"1", "60003760"} {
			m, _ = feeSetupKey(m, key)
		}
		m, cmd := feeSetupKey(m, "enter")
		if m.step != "login" || cmd == nil {
			t.Fatal("SSO did not start")
		}
		next, _ := m.Update(cmd())
		m = next.(feeSetupModel)
		if m.config.Display.Color {
			t.Fatal("SSO reload discarded --no-color")
		}
		m, _ = feeSetupKey(m, location)
		if location == "1" {
			if m.step != "buy" {
				t.Fatal("SSO skipped manual structure fees")
			}
			m, _ = feeSetupKey(m, "1.5")
			m, _ = feeSetupKey(m, "enter")
		} else if m.config.Fees.BuyBroker != .012 {
			t.Fatal("NPC buy fee did not use derived broker rate")
		}
		m, _ = feeSetupKey(m, "enter")
		c, err := LoadConfig(m.path)
		if err != nil || !m.completed || c.Fees.Setup != "eve" || c.Fees.Broker != .012 || c.Fees.SalesTax != .03375 {
			t.Fatalf("SSO completion: %#v, %v, %s", c, err, m.message)
		}
	}
	for _, failure := range []error{errors.New("browser unavailable"), context.Canceled} {
		m := feeSetupTestModel(t)
		m.login = func(_ context.Context, station string) (Config, error) {
			if station != "" {
				t.Fatalf("blank station should use default: %q", station)
			}
			return Config{}, failure
		}
		m, _ = feeSetupKey(m, "1")
		m, cmd := feeSetupKey(m, "enter")
		next, _ := m.Update(cmd())
		m = next.(feeSetupModel)
		if m.step != "choice" || m.message == "" || m.completed {
			t.Fatalf("failure did not recover: %#v", m)
		}
		m.input = "old station"
		m, _ = feeSetupKey(m, "2")
		m, _ = feeSetupKey(m, "1")
		if m.input != "" {
			t.Fatal("station input leaked into manual percentage entry")
		}
		m, _ = feeSetupKey(m, "esc")
		m, _ = feeSetupKey(m, "3")
		m, _ = feeSetupKey(m, "enter")
		if !m.completed {
			t.Fatal("could not continue with estimates after failed SSO")
		}
	}
}

func TestFeeSetupCancelsInFlightLoginBeforeLeaving(t *testing.T) {
	for _, key := range []string{"esc", "ctrl+c"} {
		m := feeSetupTestModel(t)
		m.login = func(ctx context.Context, _ string) (Config, error) {
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("login context was not cancelled")
			}
			return Config{}, ctx.Err()
		}
		m, _ = feeSetupKey(m, "1")
		m, login := feeSetupKey(m, "enter")
		m, cmd := feeSetupKey(m, key)
		if cmd != nil || m.step != "login" || !m.cancelling {
			t.Fatal("left login before cancellation finished")
		}
		next, cmd := m.Update(login())
		m = next.(feeSetupModel)
		if m.completed || (key == "esc" && (m.step != "choice" || cmd != nil)) || (key == "ctrl+c" && cmd == nil) {
			t.Fatalf("cancellation result = %#v, %v", m, cmd)
		}
	}
}

func TestFeeSetupSaveFailureAndConcurrentCompletion(t *testing.T) {
	for _, failure := range []string{"write", "completed elsewhere"} {
		m := feeSetupTestModel(t)
		m, _ = feeSetupKey(m, "3")
		if failure == "write" {
			previous := replaceConfigFile
			replaceConfigFile = func(_, _ string) error { return errors.New("disk full") }
			t.Cleanup(func() { replaceConfigFile = previous })
		} else {
			if err := updateConfig(m.path, func(c *Config) error { c.Fees.Setup = "manual"; c.Fees.Broker = .04; return nil }); err != nil {
				t.Fatal(err)
			}
		}
		before, err := os.ReadFile(m.path)
		if err != nil {
			t.Fatal(err)
		}
		m, cmd := feeSetupKey(m, "enter")
		if cmd != nil || m.completed || !strings.Contains(m.message, "Could not save") {
			t.Fatalf("save unexpectedly succeeded: %#v", m)
		}
		after, err := os.ReadFile(m.path)
		if err != nil || string(before) != string(after) {
			t.Fatal("failed setup overwrote config")
		}
		// Restore before the next iteration, not only at test cleanup.
		if failure == "write" {
			replaceConfigFile = atomicReplaceFile
		}
	}
}

func TestFeeSetupViewsFitAndExplainFees(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	m := feeSetupTestModel(t)
	m.width, m.height = 80, 18
	m.source = "eve"
	m.config.EVE.StationName = jita44Name
	for _, step := range []string{"choice", "station", "login", "location", "buy", "broker", "tax", "review"} {
		m.step = step
		view := m.View()
		if lipgloss.Width(view) > 80 || lipgloss.Height(view) > 18 || strings.Contains(view, "\x1b[") || !strings.Contains(view, "Ctrl+C") && step != "choice" {
			t.Fatalf("%s view does not fit or lacks exit:\n%s", step, view)
		}
	}
	m.step = "station"
	for _, want := range []string{"Accounting", "Broker Relations", "standings", jita44Name, "blank = default", "Manual fee setup"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("SSO view missing %q:\n%s", want, m.View())
		}
	}
	m.width = 60
	if !strings.Contains(m.View(), "Resize") {
		t.Fatal("undersized terminal not handled")
	}
}

func TestFirstRunFeeSetupAfterStartupCreatesConfig(t *testing.T) {
	doctorEnvironment(t)
	path := useIsolatedUserConfigDir(t)
	oldTerminal, oldSetup := isInteractiveTerminal, runFirstRunFeeSetup
	t.Cleanup(func() { isInteractiveTerminal, runFirstRunFeeSetup = oldTerminal, oldSetup })
	isInteractiveTerminal = func() bool { return true }
	calls := 0
	logs := t.TempDir()
	runFirstRunFeeSetup = func(_ context.Context, gotPath string, c Config) (bool, error) {
		calls++
		if gotPath != path || c.Fees.Setup != "pending" || c.Market.Marketlogs != logs || c.Display.Color {
			t.Fatalf("setup config = %s, %#v", gotPath, c)
		}
		return false, nil
	}
	var out, errOut strings.Builder
	if code := execute(nil, &out, &errOut); code != 1 || calls != 0 || !strings.Contains(errOut.String(), "market.marketlogs is empty") {
		t.Fatalf("first startup = %d, calls=%d, %s", code, calls, errOut.String())
	}
	for _, prefix := range [][]string{nil, {"tui"}, {"watch"}} {
		out.Reset()
		errOut.Reset()
		args := append(append([]string{}, prefix...), "--path", logs, "--no-color")
		if code := execute(args, &out, &errOut); code != 0 {
			t.Fatalf("setup cancellation = %d, %s", code, errOut.String())
		}
	}
	if calls != 3 {
		t.Fatalf("setup calls = %d", calls)
	}
}

func TestFirstRunFeeSetupNeverPromptsNoninteractiveCommands(t *testing.T) {
	doctorEnvironment(t)
	path := useIsolatedUserConfigDir(t)
	oldTerminal, oldSetup, oldClipboard := isInteractiveTerminal, runFirstRunFeeSetup, doctorClipboardInit
	t.Cleanup(func() {
		isInteractiveTerminal, runFirstRunFeeSetup, doctorClipboardInit = oldTerminal, oldSetup, oldClipboard
	})
	doctorClipboardInit = func() error { return nil }
	runFirstRunFeeSetup = func(context.Context, string, Config) (bool, error) {
		t.Fatal("noninteractive command prompted for fee setup")
		return false, nil
	}
	isInteractiveTerminal = func() bool { return true }
	for _, args := range [][]string{{"--help"}, {"version"}, {"--version"}, {"doctor"}} {
		var out, errOut strings.Builder
		execute(args, &out, &errOut)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%v created config: %v", args, err)
		}
	}
	isInteractiveTerminal = func() bool { return false }
	var out, errOut strings.Builder
	if code := execute([]string{"--path", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "interactive terminal") {
		t.Fatalf("noninteractive relay = %d, %s", code, errOut.String())
	}
	c, err := LoadConfig(path)
	if err != nil || c.Fees.Setup != "pending" {
		t.Fatalf("noninteractive launch consumed setup: %#v, %v", c, err)
	}
}

func TestLoginFeesSetupStateAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, initial, want string
		firstRun            bool
		cancelAt            string
		wantErr             bool
	}{
		{"setup stays pending", "pending", "pending", true, "", false},
		{"CLI login preserves pending", "pending", "pending", false, "", false},
		{"CLI login preserves estimates", "estimates", "estimates", false, "", false},
		{"CLI login preserves existing empty state", "", "", false, "", false},
		{"CLI login preserves manual", "manual", "manual", false, "", false},
		{"CLI login preserves eve", "eve", "eve", false, "", false},
		{"cancel before persistence", "pending", "pending", true, "standings", true},
		{"cancel during credential save", "pending", "pending", true, "store", true},
		{"completed elsewhere", "manual", "manual", true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := useIsolatedUserConfigDir(t)
			initial := DefaultConfig()
			initial.Fees.Setup = tc.initial
			initial.EVE = EVEConfig{CharacterID: 42, CharacterName: "Capsuleer"}
			if err := SaveConfig(path, initial); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := memoryCredentialStore{"iskrona/eve-refresh-token-42": "old-refresh"}
			var credentials credentialStore = store
			if tc.cancelAt == "store" {
				credentials = cancellingFeeSetupStore{store, cancel}
			}
			client := &eveClient{store: credentials, httpClient: &http.Client{Transport: roundTripper(func(r *http.Request) *http.Response {
				switch {
				case strings.Contains(r.URL.Path, "/universe/stations/"):
					return jsonResponse(http.StatusOK, `{"name":"Jita 4-4","owner":1000035,"race_id":1}`)
				case strings.Contains(r.URL.Path, "/skills/"):
					return jsonResponse(http.StatusOK, `{"skills":[]}`)
				case strings.Contains(r.URL.Path, "/standings/"):
					if tc.cancelAt == "standings" {
						cancel()
					}
					return jsonResponse(http.StatusOK, `[]`)
				default:
					return jsonResponse(http.StatusOK, `{"access_token":"access","refresh_token":"new-refresh"}`)
				}
			})}}
			var out strings.Builder
			var err error
			if tc.firstRun {
				err = client.loginFees(ctx, &out, "", true)
			} else {
				err = client.login(ctx, &out, "")
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("login error = %v", err)
			}
			got, err := LoadConfig(path)
			if err != nil || got.Fees.Setup != tc.want {
				t.Fatalf("saved setup = %q, %v", got.Fees.Setup, err)
			}
			if tc.wantErr && (got != initial || store["iskrona/eve-refresh-token-42"] != "old-refresh") {
				t.Fatalf("failed login persisted changes: %#v, %v", got, store)
			}
			if !tc.wantErr {
				if got.Fees.BuyBroker != initial.Fees.BuyBroker || got.Fees.BuyLocation != initial.Fees.BuyLocation || got.Fees.Broker != .03 || got.Fees.SalesTax != .075 {
					t.Fatalf("login must update only sell broker and tax: %#v", got.Fees)
				}
				if !strings.Contains(out.String(), "Buy-order fees unchanged.") {
					t.Fatal("login did not explain that buy fees are unchanged")
				}
				if tc.initial == "estimates" {
					got.Display.Color = false
					m := newMarketRelayModel(got, false, make(chan tea.Msg))
					m.width, m.height, m.snapshot = 80, 18, relayTestSnapshot()
					if !strings.Contains(m.View(), "INCLUDES ESTIMATED FEES") {
						t.Fatal("login hid the remaining buy-fee estimate")
					}
				}
				if tc.initial == "pending" && !tc.firstRun {
					oldTerminal, oldSetup := isInteractiveTerminal, runFirstRunFeeSetup
					t.Cleanup(func() { isInteractiveTerminal, runFirstRunFeeSetup = oldTerminal, oldSetup })
					isInteractiveTerminal = func() bool { return true }
					prompted := false
					runFirstRunFeeSetup = func(context.Context, string, Config) (bool, error) {
						prompted = true
						return false, nil
					}
					var stdout, stderr strings.Builder
					if code := execute([]string{"--path", t.TempDir()}, &stdout, &stderr); code != 0 || !prompted {
						t.Fatalf("login skipped pending setup: code=%d, prompted=%t, %s", code, prompted, stderr.String())
					}
				}
			}
		})
	}
}

type cancellingFeeSetupStore struct {
	credentialStore
	cancel context.CancelFunc
}

func (s cancellingFeeSetupStore) Set(service, user, value string) error {
	err := s.credentialStore.Set(service, user, value)
	s.cancel()
	return err
}

func TestFeeSetupStateValidation(t *testing.T) {
	for _, state := range []string{"", "pending", "estimates", "manual", "eve", "unknown"} {
		c := DefaultConfig()
		c.Fees.Setup = state
		if err := c.Validate(false); (err != nil) != (state == "unknown") {
			t.Fatalf("setup %q: %v", state, err)
		}
	}
}
