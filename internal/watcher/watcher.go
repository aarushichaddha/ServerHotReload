package watcher

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// ignoredDirs contains directory names that should never be watched.
var ignoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"bin":          true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
	"__pycache__":  true,
}

// ignoredExtensions contains file extensions that should be ignored.
var ignoredExtensions = map[string]bool{
	".swp":  true,
	".swo":  true,
	".swx":  true,
	".tmp":  true,
	"~":     true,
	".bak":  true,
	".orig": true,
}

// Watcher recursively watches a directory tree for file changes
// and sends events on the Events channel.
type Watcher struct {
	fsw    *fsnotify.Watcher
	root   string
	Events chan string
	Errors chan error
}

// New creates a new Watcher rooted at the given directory.
func New(root string) (*Watcher, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fsw:    fsw,
		root:   absRoot,
		Events: make(chan string, 100),
		Errors: make(chan error, 10),
	}

	if err := w.addRecursive(absRoot); err != nil {
		fsw.Close()
		return nil, err
	}

	go w.loop()
	return w, nil
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	return w.fsw.Close()
}

// addRecursive walks the directory tree and adds all eligible directories.
func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}
		if d.IsDir() {
			if shouldIgnoreDir(d.Name()) && path != root {
				return filepath.SkipDir
			}
			if err := w.fsw.Add(path); err != nil {
				slog.Warn("failed to watch directory", "path", path, "error", err)
				return nil
			}
			slog.Debug("watching directory", "path", path)
		}
		return nil
	})
}

// loop processes raw fsnotify events, filters them, and forwards relevant ones.
func (w *Watcher) loop() {
	for {
		select {
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if shouldIgnoreFile(event.Name) {
				continue
			}

			// Handle new directories: start watching them dynamically.
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if !shouldIgnoreDir(filepath.Base(event.Name)) {
						slog.Info("new directory detected, adding watcher", "path", event.Name)
						w.addRecursive(event.Name)
					}
					continue
				}
			}

			// Handle removed directories gracefully.
			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				// fsnotify automatically handles removal; just log it.
				slog.Debug("path removed/renamed", "path", event.Name)
			}

			// Only forward write/create/remove/rename events (not chmod-only).
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
				event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				// Non-blocking send.
				select {
				case w.Events <- event.Name:
				default:
				}
			}

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			select {
			case w.Errors <- err:
			default:
			}
		}
	}
}

// shouldIgnoreDir returns true if a directory name should be skipped.
func shouldIgnoreDir(name string) bool {
	return ignoredDirs[name]
}

// shouldIgnoreFile returns true if a file should be ignored based on its path.
func shouldIgnoreFile(path string) bool {
	base := filepath.Base(path)

	// Ignore hidden files (except the root).
	if strings.HasPrefix(base, ".") {
		return true
	}

	// Check extension-based ignores.
	ext := filepath.Ext(base)
	if ignoredExtensions[ext] {
		return true
	}

	// Ignore files ending with ~.
	if strings.HasSuffix(base, "~") {
		return true
	}

	return false
}
