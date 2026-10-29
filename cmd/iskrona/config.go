package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
)

var replaceConfigFile = atomicReplaceFile

type Config struct {
	Market  MarketConfig  `toml:"market"`
	Fees    FeeConfig     `toml:"fees"`
	Display DisplayConfig `toml:"display"`
	EVE     EVEConfig     `toml:"eve"`
}
type MarketConfig struct {
	Marketlogs string `toml:"marketlogs"`
	MaxJumps   int    `toml:"max_jumps"`
}
type FeeConfig struct {
	Broker   float64 `toml:"broker"`
	SalesTax float64 `toml:"sales_tax"`
}
type DisplayConfig struct {
	CopyMode string `toml:"copy_mode"`
	Color    bool   `toml:"color"`
	Theme    string `toml:"theme"`
}

var marketRelayThemes = []string{"default", "caldari", "gallente", "minmatar", "amarr"}

// EVEConfig holds non-secret metadata for the character and NPC station used
// to derive fees. The refresh token is kept in the operating system keychain.
type EVEConfig struct {
	CharacterID   int64  `toml:"character_id"`
	CharacterName string `toml:"character_name"`
	StationID     int64  `toml:"station_id"`
	StationName   string `toml:"station_name"`
}

// legacyConfig describes the pre-0.2 flat file so existing installations can
// be upgraded without losing their market folder or fee settings.
type legacyConfig struct {
	Marketlogs string  `toml:"marketlogs"`
	Jumps      int     `toml:"jumps"`
	BrokerFee  float64 `toml:"broker_fee"`
	SalesTax   float64 `toml:"sales_tax"`
}
type CLIOverrides struct {
	Path     *string
	Jumps    *int
	CopyMode *string
	NoColor  bool
}

func DefaultConfig() Config {
	return Config{Fees: FeeConfig{Broker: .02, SalesTax: .01}, Display: DisplayConfig{CopyMode: "sell", Color: true, Theme: "default"}}
}
func ConfigPath() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", fmt.Errorf("find user config directory: %w", e)
	}
	return filepath.Join(d, "iskrona", "config.toml"), nil
}
func legacyConfigPath() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "tenal", "config.toml"), nil
}
func DefaultMarketlogsPath() (string, error) {
	h, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(h, "Documents", "EVE", "logs", "Marketlogs"), nil
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		var e error
		path, e = ConfigPath()
		if e != nil {
			return Config{}, e
		}
	}
	data, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		if oldPath, oldErr := legacyConfigPath(); oldErr == nil && oldPath != path {
			if oldData, readErr := os.ReadFile(oldPath); readErr == nil {
				data, e = oldData, nil
			}
		}
	}
	if e == nil {
		c, legacy, err := decodeConfig(data)
		if err != nil {
			return Config{}, fmt.Errorf("decode config %s: %w", path, err)
		}
		if legacy {
			if err := SaveConfig(path, c); err != nil {
				return Config{}, fmt.Errorf("migrate config %s: %w", path, err)
			}
		}
		return c, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read config %s: %w", path, e)
	}
	c := DefaultConfig()
	if p, se := DefaultMarketlogsPath(); se == nil {
		if i, ee := os.Stat(p); ee == nil && i.IsDir() {
			c.Market.Marketlogs = p
		}
	}
	if e = SaveConfig(path, c); e != nil {
		return Config{}, e
	}
	return c, nil
}

// decodeConfig reads both settings formats without migrating or writing files.
func decodeConfig(data []byte) (Config, bool, error) {
	c := DefaultConfig()
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] != '[' {
		legacy := legacyConfig{BrokerFee: c.Fees.Broker, SalesTax: c.Fees.SalesTax}
		meta, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&legacy)
		if err == nil && len(meta.Undecoded()) == 0 {
			c.Market = MarketConfig{Marketlogs: legacy.Marketlogs, MaxJumps: legacy.Jumps}
			c.Fees = FeeConfig{Broker: legacy.BrokerFee, SalesTax: legacy.SalesTax}
			return c, true, nil
		}
	}
	meta, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&c)
	if err != nil {
		return Config{}, false, err
	}
	if u := meta.Undecoded(); len(u) > 0 {
		return Config{}, false, fmt.Errorf("unknown key %q", u[0].String())
	}
	return c, false, nil
}

func SaveConfig(path string, c Config) error {
	if path == "" {
		var e error
		path, e = ConfigPath()
		if e != nil {
			return e
		}
	}
	if e := c.Validate(false); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if e != nil {
		return fmt.Errorf("create temporary config for %s: %w", path, e)
	}
	tempPath := f.Name()
	defer os.Remove(tempPath)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return fmt.Errorf("secure temporary config %s: %w", path, e)
	}
	if e = toml.NewEncoder(f).Encode(c); e != nil {
		f.Close()
		return fmt.Errorf("encode config %s: %w", path, e)
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return fmt.Errorf("sync config %s: %w", path, e)
	}
	if e = f.Close(); e != nil {
		return fmt.Errorf("close config %s: %w", path, e)
	}
	if e = replaceConfigFile(tempPath, path); e != nil {
		return fmt.Errorf("replace config %s: %w", path, e)
	}
	return nil
}

// saveMaxJumps updates only the persisted distance setting. Loading the saved
// configuration first prevents temporary CLI overrides from leaking into the
// user's defaults when the TUI changes the distance.
func saveMaxJumps(path string, maxJumps int) error {
	if maxJumps < 0 {
		return fmt.Errorf("market.max_jumps must be nonnegative (got %d)", maxJumps)
	}
	c, err := LoadConfig(path)
	if err != nil {
		return err
	}
	c.Market.MaxJumps = maxJumps
	return SaveConfig(path, c)
}

// Reload saved settings so per-run overrides never become defaults.
func saveTheme(path, theme string) error {
	c, err := LoadConfig(path)
	if err != nil {
		return err
	}
	c.Display.Theme = theme
	return SaveConfig(path, c)
}

func ResolveConfig(path string, o CLIOverrides) (Config, error) {
	c, e := LoadConfig(path)
	if e != nil {
		return Config{}, e
	}
	if o.Path != nil {
		c.Market.Marketlogs = *o.Path
	}
	if o.Jumps != nil {
		c.Market.MaxJumps = *o.Jumps
	}
	if o.CopyMode != nil {
		c.Display.CopyMode = *o.CopyMode
	}
	if o.NoColor {
		c.Display.Color = false
	}
	if e = c.Validate(true); e != nil {
		return Config{}, e
	}
	return c, nil
}
func (c Config) Validate(required bool) error {
	if c.Market.MaxJumps < 0 {
		return fmt.Errorf("market.max_jumps must be nonnegative (got %d)", c.Market.MaxJumps)
	}
	if math.IsNaN(c.Fees.Broker) || c.Fees.Broker < 0 || c.Fees.Broker >= 1 {
		return fmt.Errorf("fees.broker must be in [0,1) (got %g)", c.Fees.Broker)
	}
	if math.IsNaN(c.Fees.SalesTax) || c.Fees.SalesTax < 0 || c.Fees.SalesTax >= 1 {
		return fmt.Errorf("fees.sales_tax must be in [0,1) (got %g)", c.Fees.SalesTax)
	}
	if c.Fees.Broker+c.Fees.SalesTax >= 1 {
		return errors.New("fees.broker plus fees.sales_tax must be below 1")
	}
	if c.Display.CopyMode != "buy" && c.Display.CopyMode != "sell" {
		return fmt.Errorf("display.copy_mode must be buy or sell")
	}
	if c.Display.Theme != "" && !slices.Contains(marketRelayThemes, c.Display.Theme) {
		return fmt.Errorf("display.theme must be default, caldari, gallente, minmatar or amarr")
	}
	if c.Market.Marketlogs == "" {
		if required {
			return errors.New("market.marketlogs is empty; set it in config.toml or use --path")
		}
		return nil
	}
	i, e := os.Stat(c.Market.Marketlogs)
	if e != nil {
		if required {
			return fmt.Errorf("market directory %q: %w", c.Market.Marketlogs, e)
		}
		return nil
	}
	if !i.IsDir() {
		return fmt.Errorf("market path %q is not a directory", c.Market.Marketlogs)
	}
	return nil
}
