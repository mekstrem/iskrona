package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errRetryFile = errors.New("retry market export")

type fileSignature struct {
	size    int64
	modTime time.Time
}

type pendingFile struct {
	signature fileSignature
	stable    int
}

// WatchDirectory polls a Marketlogs directory and invokes onFile for each new
// or changed, stable .txt export. Existing files are baselined and ignored.
// The callback runs synchronously, keeping output and clipboard updates ordered.
func WatchDirectory(ctx context.Context, dir string, onFile func(string) error) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	return watchDirectory(ctx, dir, onFile, scanDirectory, ticker.C, nil)
}

// watchDirectory contains the polling loop with injectable time and scanning
// seams so its stability behavior can be tested without wall-clock sleeps.
func watchDirectory(ctx context.Context, dir string, onFile func(string) error, scan func(string) (map[string]fileSignature, error), ticks <-chan time.Time, afterBaseline func()) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("marketlogs directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("marketlogs path %q is not a directory", dir)
	}

	processed, err := scan(dir)
	if err != nil {
		return err
	}
	if afterBaseline != nil {
		afterBaseline()
	}
	pending := make(map[string]pendingFile)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			current, scanErr := scan(dir)
			if scanErr != nil {
				return scanErr
			}
			for path, signature := range current {
				if previous, ok := processed[path]; ok && previous == signature {
					continue
				}
				state, ok := pending[path]
				if !ok || state.signature != signature {
					state = pendingFile{signature: signature}
				}
				state.stable++
				if state.stable >= 2 {
					if err := onFile(path); err != nil {
						if errors.Is(err, errRetryFile) {
							pending[path] = state
							continue
						}
						return fmt.Errorf("process %s: %w", filepath.Base(path), err)
					}
					processed[path] = signature
					delete(pending, path)
					continue
				}
				pending[path] = state
			}
			for path := range pending {
				if _, exists := current[path]; !exists {
					delete(pending, path)
				}
			}
		}
	}
}

func scanDirectory(dir string) (map[string]fileSignature, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("scan marketlogs directory: %w", err)
	}
	files := make(map[string]fileSignature)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files[path] = fileSignature{size: info.Size(), modTime: info.ModTime()}
	}
	return files, nil
}
