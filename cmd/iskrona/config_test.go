package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	marketlogs := filepath.Join(dir, "marketlogs")
	if err := os.Mkdir(marketlogs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "nested", "config.toml")
	want := Config{Market: MarketConfig{Marketlogs: marketlogs, MaxJumps: 7}, Fees: FeeConfig{BuyBroker: 0.012, Broker: 0.025, SalesTax: 0.011}, Display: DisplayConfig{CopyMode: "sell", Color: true}}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("loaded config = %#v, want %#v", got, want)
	}
}

func TestSaveConfigReplaceFailurePreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("original config")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	previous := replaceConfigFile
	replaceConfigFile = func(_, _ string) error { return errors.New("replace failed") }
	t.Cleanup(func() { replaceConfigFile = previous })

	err := SaveConfig(path, DefaultConfig())
	if err == nil || !strings.Contains(err.Error(), "replace failed") {
		t.Fatalf("SaveConfig error = %v, want replace failure", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("config after failed replace = %q, want %q", got, original)
	}
}

func TestSaveMaxJumpsPreservesOtherConfig(t *testing.T) {
	dir := t.TempDir()
	marketlogs := filepath.Join(dir, "marketlogs")
	if err := os.Mkdir(marketlogs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	want := Config{
		Market:  MarketConfig{Marketlogs: marketlogs, MaxJumps: 9},
		Fees:    FeeConfig{Broker: 0.025, SalesTax: 0.011},
		Display: DisplayConfig{CopyMode: "buy", Color: false, Theme: "gallente"},
		EVE:     EVEConfig{CharacterID: 42, CharacterName: "Capsuleer", StationID: 60003760, StationName: "Jita IV - Moon 4"},
	}
	initial := want
	initial.Market.MaxJumps = 2
	if err := SaveConfig(path, initial); err != nil {
		t.Fatal(err)
	}
	if err := saveMaxJumps(path, want.Market.MaxJumps); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("saved config = %#v, want %#v", got, want)
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[market]\nmax_jumps = 1\nunknown = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("LoadConfig error = %v, want unknown-key error", err)
	}
}

func TestLoadConfigCreatesDefaultsForMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "nested", "config.toml")
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultConfig()
	if got.Fees != want.Fees || got.Display != want.Display || got.Market.MaxJumps != want.Market.MaxJumps {
		t.Fatalf("LoadConfig = %#v, want default settings %#v", got, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("created config: %v", err)
	}
}

func TestLoadConfigPartialCurrentSchemaPreservesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[market]\nmax_jumps = 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Market.MaxJumps != 4 || got.Fees != DefaultConfig().Fees || got.Display != DefaultConfig().Display {
		t.Fatalf("partial config = %#v", got)
	}
}

func TestLoadConfigRejectsMalformedCurrentAndLegacyFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"current", "[market]\nmax_jumps = \"nearby\"\n"},
		{"legacy", "marketlogs = [\n"},
		{"legacy unknown key", "marketlogs = \"logs\"\nextra = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("LoadConfig succeeded, want decode error")
			}
		})
	}
}

func TestLoadConfigMigratesLegacyFlatFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	legacy := "marketlogs = \"/tmp/marketlogs\"\njumps = 4\nbroker_fee = 0.03\nsales_tax = 0.02\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Market.Marketlogs != "/tmp/marketlogs" || c.Market.MaxJumps != 4 || c.Fees.BuyBroker != 0.005 || c.Fees.Broker != 0.03 || c.Fees.SalesTax != 0.02 {
		t.Fatalf("migrated config = %#v", c)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[market]") || !strings.Contains(string(data), "max_jumps = 4") {
		t.Fatalf("config was not rewritten in the new schema: %s", data)
	}
}

func TestResolveConfigAppliesExplicitZeroJumpOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := SaveConfig(path, Config{Market: MarketConfig{Marketlogs: dir, MaxJumps: 12}, Fees: FeeConfig{Broker: 0.02, SalesTax: 0.01}, Display: DisplayConfig{CopyMode: "sell", Color: true}}); err != nil {
		t.Fatal(err)
	}
	jumps := 0
	cfg, err := ResolveConfig(path, CLIOverrides{Jumps: &jumps})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Market.MaxJumps != 0 {
		t.Fatalf("max jumps = %d, want 0", cfg.Market.MaxJumps)
	}
}

func TestResolveConfigPathOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := SaveConfig(path, Config{Market: MarketConfig{Marketlogs: dir}, Fees: FeeConfig{Broker: 0.02, SalesTax: 0.01}, Display: DisplayConfig{CopyMode: "sell", Color: true}}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveConfig(path, CLIOverrides{Path: &other})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Market.Marketlogs != other {
		t.Fatalf("marketlogs = %q, want %q", cfg.Market.Marketlogs, other)
	}
}

func TestResolveConfigRequiresMarketlogsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveConfig(path, CLIOverrides{})
	if err == nil || !strings.Contains(err.Error(), "marketlogs") {
		t.Fatalf("ResolveConfig error = %v, want marketlogs error", err)
	}
}

func TestConfigValidateFees(t *testing.T) {
	for _, cfg := range []Config{
		{Fees: FeeConfig{Broker: -0.1}},
		{Fees: FeeConfig{BuyBroker: -0.1}},
		{Fees: FeeConfig{BuyBroker: 1}},
		{Fees: FeeConfig{SalesTax: 1}},
		{Fees: FeeConfig{Broker: 0.7, SalesTax: 0.3}},
		{Market: MarketConfig{MaxJumps: -1}},
	} {
		if err := cfg.Validate(false); err == nil {
			t.Fatalf("Validate(%#v) succeeded, want error", cfg)
		}
	}
}

func TestBuyBrokerConfigCompatibility(t *testing.T) {
	for _, tc := range []struct {
		data string
		want float64
	}{
		{"[fees]\nbroker = 0.03\nsales_tax = 0.02\n", .005},
		{"[fees]\nbuy_broker = 0.012\n", .012},
		{"[fees]\nbuy_broker = 0.0\n", 0},
	} {
		c, _, err := decodeConfig([]byte(tc.data))
		if err != nil || c.Fees.BuyBroker != tc.want {
			t.Fatalf("decode %q: buy broker = %g, error = %v", tc.data, c.Fees.BuyBroker, err)
		}
	}
}

func TestConfigThemeBackwardsCompatibility(t *testing.T) {
	for _, data := range []string{
		"[display]\ncopy_mode = \"buy\"\ncolor = true\n",
		"marketlogs = \"\"\njumps = 4\nbroker_fee = 0.03\nsales_tax = 0.02\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadConfig(path)
		if err != nil || c.Display.Theme != "default" {
			t.Fatalf("old config theme = %q, error = %v", c.Display.Theme, err)
		}
	}
}

func TestSaveThemeFailurePreservesConfig(t *testing.T) {
	for _, failure := range []string{"invalid theme", "replace failure", "malformed file", "unknown key"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := SaveConfig(path, DefaultConfig()); err != nil {
				t.Fatal(err)
			}
			theme := "amarr"
			switch failure {
			case "invalid theme":
				theme = "unknown"
			case "replace failure":
				previous := replaceConfigFile
				replaceConfigFile = func(_, _ string) error { return errors.New("disk full") }
				t.Cleanup(func() { replaceConfigFile = previous })
			case "malformed file", "unknown key":
				data := "[display]\ntheme = ["
				if failure == "unknown key" {
					data = "[display]\nextra = true\n"
				}
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveTheme(path, theme); err == nil {
				t.Fatal("theme save succeeded, want error")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("failed save changed file: %q, error = %v", after, err)
			}
		})
	}
}
