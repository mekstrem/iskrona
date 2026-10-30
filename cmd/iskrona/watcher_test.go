package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWatchDirectoryProcessesNewStableExport(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 2)
	baseline := make(chan struct{})
	seen := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- watchDirectory(ctx, dir, func(path string) error {
			seen <- path
			cancel()
			return nil
		}, scanDirectory, ticks, func() { close(baseline) })
	}()
	<-baseline
	path := filepath.Join(dir, "The Forge-Test-2026.08.16 120000.txt")
	if err := os.WriteFile(path, []byte("price,bid,jumps,\n1.00,False,0,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	select {
	case got := <-seen:
		t.Fatalf("processed an export before it was stable: %q", got)
	default:
	}
	ticks <- time.Now()
	select {
	case got := <-seen:
		if got != path {
			t.Fatalf("path = %q, want %q", got, path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not process new file")
	}
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchDirectory error = %v", err)
	}
}

func TestWatchDirectoryProcessesStableExportsChronologically(t *testing.T) {
	dir := t.TempDir()
	older := time.Unix(100, 0)
	files := map[string]fileSignature{
		"z.txt": {modTime: older},
		"b.txt": {modTime: older.Add(time.Second)},
		"a.txt": {modTime: older.Add(time.Second)},
	}
	ticks := make(chan time.Time, 2)
	ticks <- time.Time{}
	ticks <- time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got []string
	scans := 0
	err := watchDirectory(ctx, dir, func(path string) error {
		got = append(got, path)
		if len(got) == len(files) {
			cancel()
		}
		return nil
	}, func(string) (map[string]fileSignature, error) {
		scans++
		if scans == 1 {
			return map[string]fileSignature{}, nil
		}
		return files, nil
	}, ticks, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("watchDirectory error = %v", err)
	}
	if want := []string{"z.txt", "a.txt", "b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("processing order = %v, want %v", got, want)
	}
}

func TestWatchDirectoryClearsPendingWhenSignatureReturnsToBaseline(t *testing.T) {
	dir := t.TempDir()
	baseline := fileSignature{size: 1}
	changed := fileSignature{size: 2}
	signatures := []fileSignature{baseline, changed, baseline, changed, changed}
	ticks := make(chan time.Time, len(signatures)-1)
	for range len(signatures) - 1 {
		ticks <- time.Time{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scans, processedAt := 0, 0
	err := watchDirectory(ctx, dir, func(string) error {
		processedAt = scans
		cancel()
		return nil
	}, func(string) (map[string]fileSignature, error) {
		if scans >= len(signatures) {
			return nil, errors.New("unexpected extra scan")
		}
		signature := signatures[scans]
		scans++
		return map[string]fileSignature{"export.txt": signature}, nil
	}, ticks, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("watchDirectory error = %v", err)
	}
	if processedAt != len(signatures) {
		t.Fatalf("processed after scan %d, want %d", processedAt, len(signatures))
	}
}

func TestWatchDirectorySupersedesOnlyObservedOlderGenerations(t *testing.T) {
	old := fileSignature{modTime: time.Unix(100, 0)}
	newer := fileSignature{modTime: time.Unix(200, 0)}
	changed := fileSignature{modTime: time.Unix(50, 0)}
	files := func(a, b *fileSignature) map[string]fileSignature {
		m := map[string]fileSignature{}
		if a != nil {
			m["a.txt"] = *a
		}
		if b != nil {
			m["b.txt"] = *b
		}
		return m
	}
	end := errors.New("end scans")
	for _, tc := range []struct {
		name      string
		scans     []map[string]fileSignature
		errors    map[string]error
		supersede bool
		want      []string
	}{
		{"staggered", []map[string]fileSignature{files(nil, nil), files(nil, &newer), files(&old, &newer), files(&old, &newer)}, nil, true, []string{"2:b.txt"}},
		{"older retry", []map[string]fileSignature{files(nil, nil), files(&old, &newer), files(&old, &newer), files(&old, &newer)}, map[string]error{"2:a.txt": errRetryFile}, true, []string{"2:a.txt", "2:b.txt"}},
		{"newer retry", []map[string]fileSignature{files(nil, nil), files(nil, &newer), files(&old, &newer), files(&old, &newer)}, map[string]error{"2:b.txt": errRetryFile}, true, []string{"2:b.txt", "3:a.txt", "3:b.txt"}},
		{"newer consumed", []map[string]fileSignature{files(nil, nil), files(nil, &newer), files(&old, &newer), files(&old, &newer)}, map[string]error{"2:b.txt": errConsumedFile}, true, []string{"2:b.txt", "3:a.txt"}},
		{"later change", []map[string]fileSignature{files(nil, nil), files(nil, &newer), files(&old, &newer), files(&old, &newer), files(&changed, &newer), files(&changed, &newer)}, nil, true, []string{"2:b.txt", "5:a.txt"}},
		{"later new file", []map[string]fileSignature{files(nil, nil), files(nil, &newer), files(nil, &newer), files(&old, &newer), files(&old, &newer)}, nil, true, []string{"2:b.txt", "4:a.txt"}},
		{"reappearance", []map[string]fileSignature{files(nil, nil), files(nil, &newer), files(&old, &newer), files(nil, &newer), files(&old, &newer), files(&old, &newer)}, nil, true, []string{"2:b.txt", "5:a.txt"}},
		{"timestamp tie", []map[string]fileSignature{files(nil, nil), files(nil, &old), files(&old, &old), files(&old, &old)}, nil, true, []string{"2:b.txt"}},
		{"ordinary watcher", []map[string]fileSignature{files(nil, nil), files(&old, &newer), files(&old, &newer), files(&old, &newer)}, map[string]error{"2:a.txt": errRetryFile}, false, []string{"2:a.txt", "2:b.txt", "3:a.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticks := make(chan time.Time, len(tc.scans))
			for range tc.scans {
				ticks <- time.Time{}
			}
			index := 0
			var got []string
			err := watchDirectoryScans(context.Background(), t.TempDir(), func(path string) error {
				key := fmt.Sprintf("%d:%s", index-1, path)
				got = append(got, key)
				return tc.errors[key]
			}, func(string) (map[string]fileSignature, error) {
				if index == len(tc.scans) {
					return nil, end
				}
				result := tc.scans[index]
				index++
				return result, nil
			}, ticks, nil, tc.supersede)
			if !errors.Is(err, end) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("error = %v, calls = %v, want %v", err, got, tc.want)
			}
		})
	}
}

func TestWatchDirectoryPreservesFatalCallbackError(t *testing.T) {
	fatal := errors.New("fatal callback")
	ticks := make(chan time.Time, 2)
	ticks <- time.Time{}
	ticks <- time.Time{}
	scans := 0
	err := watchDirectoryScans(context.Background(), t.TempDir(), func(string) error { return fatal }, func(string) (map[string]fileSignature, error) {
		scans++
		files := map[string]fileSignature{}
		if scans > 1 {
			files["export.txt"] = fileSignature{size: 1}
		}
		return files, nil
	}, ticks, nil, true)
	if !errors.Is(err, fatal) || !strings.Contains(err.Error(), "export.txt") {
		t.Fatalf("error = %v, want wrapped fatal callback error", err)
	}
}

func TestScanDirectoryIgnoresNonTextAndDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.csv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := scanDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %#v, want empty", files)
	}
}

type failingInfoEntry struct {
	os.DirEntry
	err error
}

func (e failingInfoEntry) Info() (os.FileInfo, error) { return nil, e.err }

func TestScanDirectoryReportsEntryInfoFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "export.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "healthy.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := scanDirectoryEntries(dir, []os.DirEntry{failingInfoEntry{entries[0], errors.New("info failed")}, entries[1]})
	if !errors.Is(err, errRetryScan) || !strings.Contains(err.Error(), "info failed") || !partial[filepath.Join(dir, "export.txt")].unknown || partial[filepath.Join(dir, "healthy.txt")].size != 1 {
		t.Fatalf("files = %v, scan error = %v, want unknown entry and healthy entry", partial, err)
	}
	files, err := scanDirectoryEntries(dir, []os.DirEntry{failingInfoEntry{entries[0], os.ErrNotExist}})
	if err != nil || len(files) != 0 {
		t.Fatalf("files = %v, error = %v, want confirmed disappearance", files, err)
	}
}

func TestWatchDirectoryRetriesIncompleteScansWithoutLosingState(t *testing.T) {
	ticks := make(chan time.Time, 8)
	for range 8 {
		ticks <- time.Time{}
	}
	sig := fileSignature{size: 1}
	baseline := map[string]fileSignature{"baseline.txt": sig}
	withPending := map[string]fileSignature{"baseline.txt": sig, "new.txt": sig}
	retry := fmt.Errorf("%w: permission denied", errRetryScan)
	end := errors.New("done")
	scans := 0
	var calls []string
	var reports []error
	err := watchDirectoryScansWithReporting(context.Background(), t.TempDir(), func(path string, _ fileSignature) error {
		calls = append(calls, path)
		return nil
	}, func(string) (map[string]fileSignature, error) {
		scans++
		switch scans {
		case 1, 2, 5, 6:
			return nil, retry
		case 3:
			return baseline, nil
		case 4, 7, 8:
			return withPending, nil
		default:
			return nil, end
		}
	}, ticks, nil, true, func(err error) { reports = append(reports, err) })
	if !errors.Is(err, end) || !reflect.DeepEqual(calls, []string{"new.txt"}) || len(reports) != 2 {
		t.Fatalf("error = %v, calls = %v, reports = %v", err, calls, reports)
	}
}

func TestWatchDirectoryContinuesWithUnknownEntry(t *testing.T) {
	unknown := fileSignature{unknown: true}
	sig := fileSignature{size: 1}
	retry := fmt.Errorf("%w: blocked.txt: permission denied", errRetryScan)
	end := errors.New("done")
	ticks := make(chan time.Time, 4)
	for range 4 {
		ticks <- time.Time{}
	}
	scans, reports, baselines := 0, 0, 0
	var calls []string
	err := watchDirectoryScansWithReporting(context.Background(), t.TempDir(), func(path string, _ fileSignature) error {
		calls = append(calls, path)
		return nil
	}, func(string) (map[string]fileSignature, error) {
		scans++
		switch scans {
		case 1:
			return map[string]fileSignature{"blocked.txt": unknown}, retry
		case 2, 3:
			return map[string]fileSignature{"blocked.txt": unknown, "healthy.txt": sig}, retry
		case 4:
			return map[string]fileSignature{"blocked.txt": sig, "healthy.txt": sig}, nil
		default:
			return nil, end
		}
	}, ticks, func() { baselines++ }, true, func(error) { reports++ })
	if !errors.Is(err, end) || !reflect.DeepEqual(calls, []string{"healthy.txt"}) || reports != 1 || baselines != 1 {
		t.Fatalf("error = %v, calls = %v, reports = %d, baselines = %d", err, calls, reports, baselines)
	}
}

func TestWatchDirectorySupersedesUnknownPendingGeneration(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			sig := fileSignature{size: 1, modTime: time.Unix(100, 0)}
			newer := fileSignature{size: 1, modTime: time.Unix(200, 0)}
			recovered := sig
			if changed {
				recovered.size++
			}
			unknown := fileSignature{unknown: true}
			retry := fmt.Errorf("%w: permission denied", errRetryScan)
			end := errors.New("done")
			ticks := make(chan time.Time, 6)
			for range 6 {
				ticks <- time.Time{}
			}
			scans := 0
			var calls []string
			err := watchDirectoryScansWithReporting(context.Background(), t.TempDir(), func(path string, _ fileSignature) error {
				calls = append(calls, path)
				return nil
			}, func(string) (map[string]fileSignature, error) {
				scans++
				switch scans {
				case 1:
					return map[string]fileSignature{}, nil
				case 2:
					return map[string]fileSignature{"a.txt": sig}, nil
				case 3, 4:
					return map[string]fileSignature{"a.txt": unknown, "b.txt": newer}, retry
				case 5, 6:
					return map[string]fileSignature{"a.txt": recovered, "b.txt": newer}, nil
				default:
					return nil, end
				}
			}, ticks, nil, true, func(error) {})
			want := []string{"b.txt"}
			if changed {
				want = append(want, "a.txt")
			}
			if !errors.Is(err, end) || !reflect.DeepEqual(calls, want) {
				t.Fatalf("error = %v, calls = %v", err, calls)
			}
		})
	}
}

func TestWatchDirectoryRetriesRetryableCallbackError(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 3)
	baseline := make(chan struct{})

	attempts := make(chan int, 2)
	done := make(chan error, 1)
	go func() {
		count := 0
		done <- watchDirectory(ctx, dir, func(string) error {
			count++
			attempts <- count
			if count == 1 {
				return errRetryFile
			}
			cancel()
			return nil
		}, scanDirectory, ticks, func() { close(baseline) })
	}()

	<-baseline
	if err := os.WriteFile(filepath.Join(dir, "retry.txt"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		ticks <- time.Now()
	}
	for want := 1; want <= 2; want++ {
		select {
		case got := <-attempts:
			if got != want {
				t.Fatalf("attempt = %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for attempt %d", want)
		}
	}
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchDirectory error = %v", err)
	}
}
