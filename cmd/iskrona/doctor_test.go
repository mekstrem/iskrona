package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func doctorEnvironment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestDoctorChecks(t *testing.T) {
	for _, tc := range []struct {
		name, config, export, want string
		fail                       bool
	}{
		{"valid", "", "price,bid,jumps\n20,True,0\n", "1 orders", false},
		{"zero", "", "price,bid,jumps\n", "0 orders", false},
		{"malformed export", "", "bad", "FAIL export", true},
		{"no exports", "", "", "WARN export", false},
		{"unknown key", "typo = 1", "", "unknown key", true},
		{"malformed config", "[market", "", "FAIL config", true},
		{"invalid value", "[market]\nmax_jumps = -1", "", "FAIL values", true},
		{"nan fee", "[fees]\nbroker = nan", "", "FAIL values", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := doctorEnvironment(t)
			folder := filepath.Join(dir, "exports")
			if err := os.Mkdir(folder, 0700); err != nil {
				t.Fatal(err)
			}
			config := tc.config
			if config == "" {
				config = "[market]\nmarketlogs = " + strconv.Quote(folder)
			}
			path, _ := ConfigPath()
			os.MkdirAll(filepath.Dir(path), 0700)
			os.WriteFile(path, []byte(config), 0600)
			if tc.export != "" {
				os.WriteFile(filepath.Join(folder, "orders.TXT"), []byte(tc.export), 0600)
			}
			old := doctorClipboardInit
			doctorClipboardInit = func() error { return nil }
			defer func() { doctorClipboardInit = old }()
			var out, stderr strings.Builder
			code := execute([]string{"doctor"}, &out, &stderr)
			wantCode := 0
			if tc.fail {
				wantCode = 1
			}
			if code != wantCode || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("code %d output %s stderr %s", code, &out, &stderr)
			}
			got, _ := os.ReadFile(path)
			if string(got) != config {
				t.Fatal("doctor rewrote settings")
			}
		})
	}
}

func TestDoctorMissingAndLegacyConfig(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(strconv.FormatBool(legacy), func(t *testing.T) {
			doctorEnvironment(t)
			path, _ := ConfigPath()
			folder, _ := DefaultMarketlogsPath()
			os.MkdirAll(folder, 0700)
			var oldPath, config string
			if legacy {
				oldPath, _ = legacyConfigPath()
				os.MkdirAll(filepath.Dir(oldPath), 0700)
				config = "marketlogs = " + strconv.Quote(folder)
				os.WriteFile(oldPath, []byte(config), 0600)
			}
			var out strings.Builder
			if !runDoctor(&out, path, func() error { return errors.New("unavailable") }) {
				t.Fatal(out.String())
			}
			if !strings.Contains(out.String(), "WARN config") || !strings.Contains(out.String(), "WARN clipboard") {
				t.Fatal(out.String())
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("config created", err)
			}
			if legacy {
				got, _ := os.ReadFile(oldPath)
				if string(got) != config {
					t.Fatal("legacy rewritten")
				}
			}
		})
	}
}

func TestDoctorFolderFailures(t *testing.T) {
	dir := doctorEnvironment(t)
	path := filepath.Join(dir, "config.toml")
	for _, folder := range []string{"", filepath.Join(dir, "missing"), path} {
		os.WriteFile(path, []byte("[market]\nmarketlogs = "+strconv.Quote(folder)), 0600)
		var out strings.Builder
		if runDoctor(&out, path, func() error { return nil }) || !strings.Contains(out.String(), "FAIL folder") {
			t.Fatal(out.String())
		}
	}
	os.Remove(path)
	var out strings.Builder
	if runDoctor(&out, path, func() error { return nil }) || !strings.Contains(out.String(), "FAIL folder") {
		t.Fatal(out.String())
	}
}

func TestDoctorUnreadableConfig(t *testing.T) {
	dir := doctorEnvironment(t)
	var out strings.Builder
	if runDoctor(&out, dir, func() error { return nil }) || !strings.Contains(out.String(), "FAIL config") {
		t.Fatal(out.String())
	}
}

func TestDoctorNewestExport(t *testing.T) {
	dir := doctorEnvironment(t)
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("[market]\nmarketlogs = "+strconv.Quote(dir)), 0600)
	stamp := time.Now().Add(-time.Hour)
	for _, name := range []string{"a.TXT", "b.txt"} {
		file := filepath.Join(dir, name)
		os.WriteFile(file, []byte("price,bid,jumps\n"), 0600)
		os.Chtimes(file, stamp, stamp)
	}
	var out strings.Builder
	if !runDoctor(&out, path, func() error { return nil }) || !strings.Contains(out.String(), "a.TXT: 0 orders") {
		t.Fatal(out.String())
	}
	os.Chtimes(filepath.Join(dir, "b.txt"), stamp.Add(time.Minute), stamp.Add(time.Minute))
	out.Reset()
	if !runDoctor(&out, path, func() error { return nil }) || !strings.Contains(out.String(), "b.txt: 0 orders") {
		t.Fatal(out.String())
	}
}
