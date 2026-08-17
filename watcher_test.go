package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchDirectoryProcessesNewStableExport(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan string, 1)
	go func() {
		_ = WatchDirectory(ctx, dir, func(path string) error {
			seen <- path
			cancel()
			return nil
		})
	}()
	time.Sleep(300 * time.Millisecond)
	path := filepath.Join(dir, "The Forge-Test-2026.08.16 120000.txt")
	if err := os.WriteFile(path, []byte("price,bid,jumps,\n1.00,False,0,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seen:
		if got != path {
			t.Fatalf("path = %q, want %q", got, path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not process new file")
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

func TestWatchDirectoryRetriesRetryableCallbackError(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	attempts := make(chan int, 2)
	done := make(chan error, 1)
	go func() {
		count := 0
		done <- WatchDirectory(ctx, dir, func(string) error {
			count++
			attempts <- count
			if count == 1 {
				return errRetryFile
			}
			cancel()
			return nil
		})
	}()

	// Let WatchDirectory establish its initial baseline before creating the
	// export, matching the documented behavior that existing files are ignored.
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "retry.txt"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
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
