package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var doctorClipboardInit = InitClipboard

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Check local settings and market exports without changing files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := ConfigPath()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "FAIL config: %v; set your user config directory.\n", err)
			return commandError(errors.New("doctor found local problems"))
		}
		if !runDoctor(cmd.OutOrStdout(), path, doctorClipboardInit) {
			return commandError(errors.New("doctor found local problems"))
		}
		return nil
	}}
}

func runDoctor(out io.Writer, path string, initClipboard func() error) bool {
	ok := true
	report := func(status, check, detail string) {
		fmt.Fprintf(out, "%s %s: %s\n", status, check, detail)
		if status == "FAIL" {
			ok = false
		}
	}
	c := DefaultConfig()
	data, err := os.ReadFile(path)
	legacyPath := false
	if errors.Is(err, os.ErrNotExist) {
		old, oldErr := legacyConfigPath()
		if oldErr == nil && old != path {
			if oldData, readErr := os.ReadFile(old); !errors.Is(readErr, os.ErrNotExist) {
				path, data, err, legacyPath = old, oldData, readErr, true
			}
		}
	}
	configOK := true
	if errors.Is(err, os.ErrNotExist) {
		report("WARN", "config", fmt.Sprintf("%s is missing; using in-memory defaults. Create settings there to persist your market folder.", path))
		c.Market.Marketlogs, err = DefaultMarketlogsPath()
		if err != nil {
			report("FAIL", "folder", fmt.Sprintf("cannot locate default market folder: %v; set market.marketlogs in %s.", err, path))
			configOK = false
		}
	} else if err != nil {
		report("FAIL", "config", fmt.Sprintf("cannot read %s: %v; fix its path or read permissions.", path, err))
		configOK = false
	} else {
		var legacy bool
		c, legacy, err = decodeConfig(data)
		if err != nil {
			report("FAIL", "config", fmt.Sprintf("%s: %v; correct the TOML and supported setting names.", path, err))
			configOK = false
		} else if legacy || legacyPath {
			report("WARN", "config", fmt.Sprintf("legacy settings at %s; move settings to the current iskrona config format/location. No migration performed.", path))
		} else {
			report("PASS", "config", path)
		}
	}
	if configOK {
		// Validate values separately so folder failures get their own actionable check.
		values := c
		values.Market.Marketlogs = ""
		if err := values.Validate(false); err != nil {
			report("FAIL", "values", fmt.Sprintf("%v; correct the value in %s.", err, path))
		} else {
			report("PASS", "values", "settings values are valid")
		}
		folder := c.Market.Marketlogs
		entries, err := os.ReadDir(folder)
		if err != nil {
			report("FAIL", "folder", fmt.Sprintf("cannot read market directory %q: %v; set market.marketlogs in %s to an existing readable directory.", folder, err, path))
		} else {
			report("PASS", "folder", folder)
			var newest os.FileInfo
			for _, entry := range entries {
				if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					report("FAIL", "export", fmt.Sprintf("cannot inspect %s: %v; fix export read permissions.", entry.Name(), err))
					continue
				}
				if newest == nil || info.ModTime().After(newest.ModTime()) || (info.ModTime().Equal(newest.ModTime()) && info.Name() < newest.Name()) {
					newest = info
				}
			}
			if newest == nil {
				report("WARN", "export", "no .txt exports found; export market orders from EVE to this folder.")
			} else {
				file := filepath.Join(folder, newest.Name())
				f, err := os.Open(file)
				if err != nil {
					report("FAIL", "export", fmt.Sprintf("cannot read %s: %v; fix export read permissions.", file, err))
				} else {
					orders, parseErr := ParseMarketCSV(f)
					f.Close()
					if parseErr != nil {
						report("FAIL", "export", fmt.Sprintf("%s: %v; retry after EVE finishes exporting, or export fresh market data.", file, parseErr))
					} else {
						report("PASS", "export", fmt.Sprintf("%s: %d orders", file, len(orders)))
					}
				}
			}
		}
	}
	if err := initClipboard(); err != nil {
		report("WARN", "clipboard", fmt.Sprintf("%v; enable your platform clipboard service to copy recommendations. Manual copying remains available.", err))
	} else {
		report("PASS", "clipboard", "clipboard initialization available (nothing copied)")
	}
	return ok
}
