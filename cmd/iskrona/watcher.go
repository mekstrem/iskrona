package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var errRetryFile = errors.New("retry market export")
var errConsumedFile = errors.New("market export consumed without publication")
var errRetryScan = errors.New("retry marketlogs scan")

type fileSignature struct {
	size    int64
	modTime time.Time
	unknown bool
}

type pendingFile struct {
	signature  fileSignature
	stable     int
	superseded bool
}

// WatchDirectory polls a Marketlogs directory and invokes onFile for each new
// or changed, stable .txt export. Existing files are baselined and ignored.
// The callback runs synchronously, keeping output and clipboard updates ordered.
func WatchDirectory(ctx context.Context, dir string, onFile func(string) error) error {
	return watchDirectoryWithBaseline(ctx, dir, onFile, nil)
}

func watchDirectoryWithBaseline(ctx context.Context, dir string, onFile func(string) error, afterBaseline func()) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	return watchDirectory(ctx, dir, onFile, scanDirectory, ticker.C, afterBaseline)
}

func watchDirectoryWithOptions(ctx context.Context, dir string, onFile func(string, fileSignature) error, afterBaseline func(), reportScanError func(error)) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	return watchDirectoryScansWithReporting(ctx, dir, onFile, scanDirectory, ticker.C, afterBaseline, true, reportScanError)
}

// watchDirectory contains the polling loop with injectable time and scanning
// seams so its stability behavior can be tested without wall-clock sleeps.
func watchDirectory(ctx context.Context, dir string, onFile func(string) error, scan func(string) (map[string]fileSignature, error), ticks <-chan time.Time, afterBaseline func()) error {
	return watchDirectoryScans(ctx, dir, onFile, scan, ticks, afterBaseline, false)
}

func watchDirectoryScans(ctx context.Context, dir string, onFile func(string) error, scan func(string) (map[string]fileSignature, error), ticks <-chan time.Time, afterBaseline func(), supersede bool) error {
	return watchDirectoryScansWithSignature(ctx, dir, func(path string, _ fileSignature) error { return onFile(path) }, scan, ticks, afterBaseline, supersede)
}

func watchDirectoryScansWithSignature(ctx context.Context, dir string, onFile func(string, fileSignature) error, scan func(string) (map[string]fileSignature, error), ticks <-chan time.Time, afterBaseline func(), supersede bool) error {
	return watchDirectoryScansWithReporting(ctx, dir, onFile, scan, ticks, afterBaseline, supersede, func(err error) { fmt.Fprintln(os.Stderr, err) })
}

func watchDirectoryScansWithReporting(ctx context.Context, dir string, onFile func(string, fileSignature) error, scan func(string) (map[string]fileSignature, error), ticks <-chan time.Time, afterBaseline func(), supersede bool, reportScanError func(error)) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("marketlogs directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("marketlogs path %q is not a directory", dir)
	}

	var processed map[string]fileSignature
	reported := false
	for {
		processed, err = scan(dir)
		if err == nil || (errors.Is(err, errRetryScan) && processed != nil) {
			if err != nil && !reported {
				reportScanError(err)
				reported = true
			}
			break
		}
		if !errors.Is(err, errRetryScan) {
			return err
		}
		if !reported {
			reportScanError(err)
			reported = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
		}
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
				if errors.Is(scanErr, errRetryScan) {
					if !reported {
						reportScanError(scanErr)
						reported = true
					}
					if current == nil {
						continue
					}
				} else {
					return scanErr
				}
			} else {
				reported = false
			}
			paths := make([]string, 0, len(current))
			for path, signature := range current {
				if !signature.unknown {
					paths = append(paths, path)
				}
			}
			sort.Slice(paths, func(i, j int) bool {
				a, b := current[paths[i]].modTime, current[paths[j]].modTime
				if a.Equal(b) {
					return paths[i] < paths[j]
				}
				return a.Before(b)
			})
			for _, path := range paths {
				signature := current[path]
				if previous, ok := processed[path]; ok && previous.unknown {
					processed[path] = signature
					continue
				}
				if previous, ok := processed[path]; ok && previous == signature {
					delete(pending, path)
					continue
				}
				state, ok := pending[path]
				if ok && state.superseded && state.signature == signature {
					processed[path] = signature
					delete(pending, path)
					continue
				}
				if !ok || state.signature != signature {
					state = pendingFile{signature: signature}
				}
				state.stable++
				pending[path] = state
			}
			for path := range pending {
				if _, exists := current[path]; !exists {
					delete(pending, path)
				}
			}
			for path := range processed {
				if _, exists := current[path]; !exists {
					delete(processed, path)
				}
			}
			for i, path := range paths {
				state, ok := pending[path]
				if !ok || state.stable < 2 {
					continue
				}
				err := onFile(path, state.signature)
				if errors.Is(err, errRetryFile) {
					continue
				}
				if err != nil && !errors.Is(err, errConsumedFile) {
					return fmt.Errorf("process %s: %w", filepath.Base(path), err)
				}
				processed[path] = state.signature
				delete(pending, path)
				if supersede && err == nil {
					for _, older := range paths[:i] {
						if previous, ok := pending[older]; ok {
							processed[older] = previous.signature
							delete(pending, older)
						}
					}
					for older, previous := range pending {
						if !current[older].unknown {
							continue
						}
						a, b := previous.signature.modTime, state.signature.modTime
						if a.Before(b) || (a.Equal(b) && older < path) {
							previous.superseded = true
							pending[older] = previous
						}
					}
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
	return scanDirectoryEntries(dir, entries)
}

func scanDirectoryEntries(dir string, entries []os.DirEntry) (map[string]fileSignature, error) {
	files := make(map[string]fileSignature)
	var scanErr error
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			files[path] = fileSignature{unknown: true}
			if scanErr == nil {
				scanErr = fmt.Errorf("%w: stat market export %q: %w", errRetryScan, path, err)
			}
			continue
		}
		files[path] = fileSignature{size: info.Size(), modTime: info.ModTime()}
	}
	return files, scanErr
}
