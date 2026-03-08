package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aarushichaddha/hotreload/internal/debouncer"
	"github.com/aarushichaddha/hotreload/internal/livereload"
	"github.com/aarushichaddha/hotreload/internal/runner"
	"github.com/aarushichaddha/hotreload/internal/watcher"
)

const (
	debounceInterval = 200 * time.Millisecond
)

func main() {
	root := flag.String("root", ".", "Directory to watch for file changes (including all subfolders)")
	buildCmd := flag.String("build", "", "Command used to build the project when a change is detected")
	execCmd := flag.String("exec", "", "Command used to run the built server after a successful build")
	verbose := flag.Bool("verbose", false, "Enable debug logging")
	lrPort := flag.Int("livereload", 35729, "Port for the live-reload SSE server (0 to disable)")
	flag.Parse()

	if *buildCmd == "" || *execCmd == "" {
		fmt.Fprintf(os.Stderr, "Usage: hotreload --root <dir> --build \"<cmd>\" --exec \"<cmd>\"\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Configure logging.
	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	slog.Info("hotreload starting",
		"root", *root,
		"build", *buildCmd,
		"exec", *execCmd,
	)

	// Validate root directory exists.
	info, err := os.Stat(*root)
	if err != nil || !info.IsDir() {
		slog.Error("root path is not a valid directory", "root", *root, "error", err)
		os.Exit(1)
	}

	// Initialize the file watcher.
	w, err := watcher.New(*root)
	if err != nil {
		slog.Error("failed to initialize watcher", "error", err)
		os.Exit(1)
	}
	defer w.Close()

	// Initialize the debouncer.
	deb := debouncer.New(debounceInterval)

	// Initialize the runner.
	r := runner.New(*buildCmd, *execCmd)

	// Start the live-reload server if enabled.
	if *lrPort > 0 {
		lr := livereload.New()
		addr := fmt.Sprintf(":%d", *lrPort)
		if err := lr.Start(addr); err != nil {
			slog.Error("failed to start livereload server", "error", err)
			os.Exit(1)
		}
		r.OnRestart(lr.Reload)
		slog.Info("livereload enabled",
			"port", *lrPort,
			"script_tag", livereload.ScriptTag(*lrPort),
		)
	}

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Trigger the first build immediately.
	slog.Info("triggering initial build")
	r.Trigger()

	// Main event loop.
	for {
		select {
		case path := <-w.Events:
			slog.Info("file changed", "path", path)
			deb.Signal()

		case err := <-w.Errors:
			slog.Error("watcher error", "error", err)

		case <-deb.Events():
			slog.Info("change detected, rebuilding...")
			r.Trigger()

		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			r.Stop()
			slog.Info("hotreload stopped")
			return
		}
	}
}
