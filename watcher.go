package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("marketlogs directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("marketlogs path %q is not a directory", dir)
	}

	processed, err := scanDirectory(dir)
	if err != nil {
		return err
	}
	pending := make(map[string]pendingFile)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, scanErr := scanDirectory(dir)
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
