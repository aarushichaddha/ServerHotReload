package watcher_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aarushichaddha/hotreload/internal/watcher"
)

func TestWatcherDetectsFileChange(t *testing.T) {
	dir := t.TempDir()

	// Create an initial file so the directory isn't empty.
	initial := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(initial, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	w, err := watcher.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Modify the file.
	time.Sleep(100 * time.Millisecond) // Give watcher time to register.
	if err := os.WriteFile(initial, []byte("package main\n// changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case path := <-w.Events:
		if path != initial {
			t.Logf("got event for %s (expected %s), but still valid", path, initial)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected file change event, got timeout")
	}
}

func TestWatcherDetectsNewFile(t *testing.T) {
	dir := t.TempDir()

	w, err := watcher.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	time.Sleep(100 * time.Millisecond)

	newFile := filepath.Join(dir, "new.go")
	if err := os.WriteFile(newFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-w.Events:
		// Expected.
	case <-time.After(2 * time.Second):
		t.Fatal("expected new file event, got timeout")
	}
}

func TestWatcherIgnoresGitDir(t *testing.T) {
	dir := t.TempDir()

	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	w, err := watcher.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	time.Sleep(100 * time.Millisecond)

	// Write to .git directory -- should be ignored.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-w.Events:
		t.Fatal("should not get events from .git directory")
	case <-time.After(500 * time.Millisecond):
		// Expected: no event.
	}
}

func TestWatcherDetectsNewDirectory(t *testing.T) {
	dir := t.TempDir()

	w, err := watcher.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	time.Sleep(100 * time.Millisecond)

	// Create a new subdirectory.
	subDir := filepath.Join(dir, "pkg")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Give the watcher time to pick up the new directory.
	time.Sleep(200 * time.Millisecond)

	// Write a file in the new subdirectory.
	newFile := filepath.Join(subDir, "lib.go")
	if err := os.WriteFile(newFile, []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-w.Events:
		// Expected: detected change in dynamically added directory.
	case <-time.After(2 * time.Second):
		t.Fatal("expected event from new subdirectory, got timeout")
	}
}
