package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// gracefulTimeout is how long we wait after SIGTERM before sending SIGKILL.
	gracefulTimeout = 3 * time.Second
	// crashThreshold is the minimum run time before a restart is considered non-crashing.
	crashThreshold = 2 * time.Second
	// maxBackoff is the maximum delay between restart attempts when crash-looping.
	maxBackoff = 30 * time.Second
)

// Runner manages the build-and-exec lifecycle.
type Runner struct {
	buildCmd  string
	execCmd   string
	onRestart func() // optional callback invoked after a successful server start

	mu        sync.Mutex
	cancel    context.CancelFunc // cancels the current build+exec cycle
	execProc  *os.Process        // the currently running server process
	crashBack time.Duration      // current backoff for crash-loop detection
}

// New creates a Runner with the given build and exec commands.
func New(buildCmd, execCmd string) *Runner {
	return &Runner{
		buildCmd: buildCmd,
		execCmd:  execCmd,
	}
}

// OnRestart sets a callback that is invoked every time the server
// is successfully started (useful for live-reload notifications).
func (r *Runner) OnRestart(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onRestart = fn
}

// Trigger starts a new build+exec cycle. If a previous cycle is running,
// it is cancelled first (build aborted, server killed).
func (r *Runner) Trigger() {
	r.mu.Lock()

	// Cancel any in-flight cycle.
	if r.cancel != nil {
		r.cancel()
	}

	// Kill any running server process.
	r.killServer()

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.mu.Unlock()

	go r.run(ctx)
}

// Stop terminates the current cycle and server.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.killServer()
}

// run executes one build+exec cycle within the given context.
func (r *Runner) run(ctx context.Context) {
	// Apply crash-loop backoff if needed.
	r.mu.Lock()
	backoff := r.crashBack
	r.mu.Unlock()

	if backoff > 0 {
		slog.Warn("crash loop detected, waiting before restart", "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}

	// --- Build phase ---
	slog.Info("building project", "cmd", r.buildCmd)
	if err := r.execCommand(ctx, r.buildCmd); err != nil {
		if ctx.Err() != nil {
			slog.Info("build cancelled")
			return
		}
		slog.Error("build failed", "error", err)
		return
	}
	slog.Info("build succeeded")

	// Check if context was cancelled between build and exec.
	if ctx.Err() != nil {
		return
	}

	// --- Exec phase ---
	slog.Info("starting server", "cmd", r.execCmd)
	startTime := time.Now()

	cmd := r.buildExecCmd(ctx, r.execCmd)

	// Stream stdout/stderr in real time.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		slog.Error("failed to start server", "error", err)
		return
	}

	r.mu.Lock()
	r.execProc = cmd.Process
	onRestart := r.onRestart
	r.mu.Unlock()

	// Notify live-reload clients that the server is up.
	if onRestart != nil {
		// Small delay to let the server bind its port before browsers reload.
		time.AfterFunc(300*time.Millisecond, onRestart)
	}

	// Wait for the process to finish (or be killed).
	err := cmd.Wait()

	r.mu.Lock()
	r.execProc = nil
	r.mu.Unlock()

	if ctx.Err() != nil {
		// We killed it intentionally; no crash-loop logic needed.
		return
	}

	// Process exited on its own -- check if it was a crash.
	elapsed := time.Since(startTime)
	if err != nil && elapsed < crashThreshold {
		r.mu.Lock()
		if r.crashBack == 0 {
			r.crashBack = 1 * time.Second
		} else {
			r.crashBack *= 2
			if r.crashBack > maxBackoff {
				r.crashBack = maxBackoff
			}
		}
		r.mu.Unlock()
		slog.Error("server crashed shortly after starting",
			"error", err, "uptime", elapsed, "next_backoff", r.crashBack)
	} else {
		// Reset backoff on a clean exit or long-lived process.
		r.mu.Lock()
		r.crashBack = 0
		r.mu.Unlock()
		if err != nil {
			slog.Error("server exited with error", "error", err)
		} else {
			slog.Info("server exited cleanly")
		}
	}
}

// execCommand runs a shell command synchronously, respecting context cancellation.
func (r *Runner) execCommand(ctx context.Context, command string) error {
	parts := splitCommand(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = ""
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Use process group so we can kill the entire tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Context cancelled: kill the process group.
		killProcessGroup(cmd.Process)
		<-done
		return ctx.Err()
	}
}

// buildExecCmd creates an *exec.Cmd for the server process with
// a process group so we can kill the entire tree later.
func (r *Runner) buildExecCmd(_ context.Context, command string) *exec.Cmd {
	parts := splitCommand(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// killServer kills the current server process and all its children.
func (r *Runner) killServer() {
	if r.execProc == nil {
		return
	}

	pid := r.execProc.Pid
	slog.Info("stopping server", "pid", pid)

	// First, try graceful shutdown with SIGTERM to the process group.
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// Process already gone.
		r.execProc = nil
		return
	}

	// Send SIGTERM to the entire process group.
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Wait briefly for graceful shutdown.
	done := make(chan struct{})
	go func() {
		r.execProc.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("server stopped gracefully", "pid", pid)
	case <-time.After(gracefulTimeout):
		slog.Warn("server did not stop gracefully, sending SIGKILL", "pid", pid)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}

	r.execProc = nil
}

// killProcessGroup kills an entire process group by PGID.
func killProcessGroup(p *os.Process) {
	if p == nil {
		return
	}
	pgid, err := syscall.Getpgid(p.Pid)
	if err != nil {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// splitCommand splits a shell command string into parts.
// For simple commands it splits on spaces; for complex ones it uses sh -c.
func splitCommand(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	// If the command contains shell metacharacters, use sh -c.
	if strings.ContainsAny(cmd, "|&;<>()$`\\\"'") {
		return []string{"sh", "-c", cmd}
	}
	return strings.Fields(cmd)
}
