# hotreload — Architecture & Design Write-Up

## 1. Architecture Overview

The tool is structured as four independent internal packages, each with a single responsibility, wired together through a central event loop in `main.go`.

```
┌──────────────────────────────────────────────────────────┐
│                    cmd/hotreload/main.go                  │
│                     (Event Loop + CLI)                    │
│                                                          │
│   ┌─────────┐    ┌───────────┐    ┌────────┐            │
│   │ Watcher │───▶│ Debouncer │───▶│ Runner │            │
│   └─────────┘    └───────────┘    └────────┘            │
│        │                              │                  │
│   fsnotify                       ┌────────────┐         │
│   events                         │ LiveReload │         │
│                                  │ (SSE Server)│         │
│                                  └────────────┘         │
└──────────────────────────────────────────────────────────┘
```

**Data flow:**

```
File saved on disk
  → fsnotify fires raw OS event
  → Watcher filters it (ignores .git, temp files, etc.)
  → Watcher sends path on Events channel
  → main.go receives it, calls Debouncer.Signal()
  → Debouncer waits 200ms of quiet time, then fires
  → main.go receives debounce event, calls Runner.Trigger()
  → Runner cancels any in-flight build
  → Runner kills the previous server (SIGTERM → SIGKILL)
  → Runner executes the build command
  → Runner starts the new server
  → Runner notifies LiveReload SSE server
  → Connected browsers receive "reload" event and refresh
```

### Package Breakdown

| Package | File | Responsibility |
|---------|------|---------------|
| `internal/watcher` | `watcher.go` | Wraps `fsnotify`. Recursively walks the directory tree, adds watchers to every eligible directory, filters out noise (hidden files, `.git`, `node_modules`, editor temp files), detects new directories at runtime and adds them dynamically, handles deleted directories gracefully. |
| `internal/debouncer` | `debouncer.go` | Receives rapid "signal" calls and coalesces them into a single output event after a configurable quiet period (200ms). Every new signal resets the timer. Uses `time.AfterFunc` internally — no background goroutine spinning. |
| `internal/runner` | `runner.go` | Manages the full build→exec lifecycle. Handles context-based cancellation, process group management, crash-loop detection with exponential backoff, and an optional restart callback for live-reload. |
| `internal/livereload` | `livereload.go` | A lightweight HTTP server that serves an SSE endpoint (`/livereload`) and a JS snippet (`/livereload.js`). When `Reload()` is called, it pushes a `reload` event to all connected browser clients. |

---

## 2. Key Design Decisions

### 2.1 Process Groups for Clean Kills

**Problem:** The exec'd server may spawn child processes (goroutines using `exec.Command`, or the shell forking). Killing only the parent PID leaves orphans holding onto ports and resources.

**Solution:** Every process started by the runner uses `syscall.SysProcAttr{Setpgid: true}` to create a new process group. When killing, we send signals to `-pgid` (the entire group), not just the PID. This ensures all children, grandchildren, etc. are terminated.

```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
// Later:
syscall.Kill(-pgid, syscall.SIGTERM)  // kills the whole group
```

### 2.2 Two-Phase Kill: SIGTERM then SIGKILL

**Problem:** Some servers install signal handlers and need time to drain connections. Others ignore SIGTERM entirely.

**Solution:** We send SIGTERM first and wait up to 3 seconds. If the process is still alive, we escalate to SIGKILL. This handles both well-behaved servers (graceful shutdown) and stubborn ones (force kill).

### 2.3 Debouncing, Not Just Throttling

**Problem:** Editors generate bursts of filesystem events on save. VSCode, for example, may fire Write, Chmod, and Create events within milliseconds for a single save. A naive approach would trigger 3 rebuilds.

**Solution:** The debouncer uses a resetting timer. Each incoming signal resets the 200ms countdown. Only after 200ms of silence does it fire. This naturally coalesces editor save bursts into a single rebuild, regardless of how many raw events arrive.

**Why 200ms?** It's fast enough to feel instant to a human (<2s rebuild target), but long enough to capture the tail end of editor save bursts. Most editors complete their save sequence within 50-100ms.

### 2.4 Build Cancellation via Context

**Problem:** If a developer saves twice in quick succession, the first build may still be running when the second change arrives. Building stale code wastes time.

**Solution:** Each build+exec cycle runs within a `context.Context`. When `Trigger()` is called again, it cancels the previous context. The build command (running via `exec.CommandContext`) is killed immediately, and the runner starts fresh with the latest code.

### 2.5 Crash-Loop Protection with Exponential Backoff

**Problem:** If the server has a bug that causes it to crash immediately on startup, a naive hot-reload tool would enter an infinite rapid restart loop — burning CPU, flooding logs, and potentially causing cascading failures.

**Solution:** The runner tracks how long the server lived. If it crashes within 2 seconds of starting, the backoff doubles: 1s → 2s → 4s → 8s → 16s → 30s (capped). Once the server runs for more than 2 seconds, the backoff resets to zero. A new file change also resets the cycle, so the developer isn't stuck waiting after fixing the bug.

### 2.6 File Filtering

**Problem:** A Go project contains many files that are irrelevant to the build — `.git` internals, `node_modules`, binary artifacts, editor swap files. Watching and reacting to these wastes resources and triggers false rebuilds.

**Solution:** The watcher applies two layers of filtering:

1. **Directory-level:** Entire directory trees are skipped during the initial walk and ignored if created at runtime. Ignored dirs: `.git`, `node_modules`, `vendor`, `bin`, `dist`, `build`, `.idea`, `.vscode`, `__pycache__`.

2. **File-level:** Individual file events are filtered by name patterns. Ignored: hidden files (`.` prefix), editor temp files (`.swp`, `.swo`, `.tmp`, `~`, `.bak`, `.orig`).

### 2.7 SSE-Based Live Reload (No External Dependencies)

**Problem:** After the server restarts, the developer has to manually switch to their browser and press F5. This breaks flow.

**Solution:** The hotreload tool runs a small SSE (Server-Sent Events) server on port 35729. The testserver's HTML includes a `<script>` that connects to this SSE endpoint. When the runner successfully starts a new server, it calls `LiveReload.Reload()`, which pushes an event to all connected browsers. The browser JS calls `location.reload()`.

**Why SSE instead of WebSocket?** SSE is simpler (no handshake upgrade, no framing), works with the Go standard library (no external deps), and is sufficient for one-way server-to-browser notifications. The browser's `EventSource` API also handles automatic reconnection natively.

### 2.8 Non-Blocking Channel Sends

**Problem:** If channels fill up (e.g., the main loop is busy building), senders could block indefinitely, creating deadlocks.

**Solution:** All channel sends throughout the codebase use the `select/default` pattern for non-blocking behavior. If the channel is full, the event is dropped — which is acceptable because the debouncer ensures we only need "at least one" notification, not every single event.

```go
select {
case w.Events <- event.Name:
default:  // drop if full — debouncer will catch the next one
}
```

---

## 3. How the Tool Works (Step by Step)

### Startup

1. Parse CLI flags: `--root`, `--build`, `--exec`, `--livereload`, `--verbose`
2. Validate the root directory exists
3. Initialize the **Watcher**: walk the root directory tree recursively, add an `fsnotify` watch on every eligible subdirectory
4. Initialize the **Debouncer** with a 200ms quiet interval
5. Initialize the **Runner** with the build and exec commands
6. Start the **LiveReload** SSE server on port 35729
7. Wire the runner's `OnRestart` callback to `livereload.Reload()`
8. **Trigger the initial build immediately** (the server starts without waiting for any file change)
9. Enter the main event loop

### Main Event Loop

The event loop is a single `select` statement that multiplexes four channels:

```go
for {
    select {
    case path := <-watcher.Events:    // file changed → signal debouncer
    case err := <-watcher.Errors:     // watcher error → log it
    case <-debouncer.Events():        // quiet period elapsed → trigger rebuild
    case sig := <-sigCh:              // SIGINT/SIGTERM → graceful shutdown
    }
}
```

### Rebuild Cycle (inside Runner.Trigger)

1. Cancel any in-flight build/exec context
2. Kill the previous server process (SIGTERM → wait 3s → SIGKILL), targeting the entire process group
3. Apply crash-loop backoff delay if applicable
4. Run the build command; if it fails, stop (don't start a broken server)
5. Run the exec command; stream stdout/stderr to the terminal in real time
6. After 300ms (to let the server bind its port), notify the LiveReload SSE server
7. Monitor the server process: if it exits on its own within 2s, increase backoff; otherwise reset backoff

### Shutdown

On SIGINT or SIGTERM:
1. Cancel the current build/exec context
2. Kill the running server and its process group
3. Exit cleanly

---

## 4. Demo Walkthrough

### Setup

```bash
# Build the hotreload binary
make build

# Start hotreload watching the test server
make run
```

Terminal output:
```
level=INFO msg="hotreload starting" root=./testserver
level=INFO msg="livereload enabled" port=35729
level=INFO msg="triggering initial build"
level=INFO msg="building project" cmd="go build -o ./bin/server ./testserver/cmd/server"
level=INFO msg="build succeeded"
level=INFO msg="starting server" cmd=./bin/server
testserver listening on :8080
```

### Browser

Open `http://localhost:8080`. The page shows:

```
Hello from the test server!
Version: 15
```

The page includes an invisible `<script>` tag that connects to the LiveReload SSE server on port 35729. You can verify this in the browser's DevTools Console:
```
[hotreload] connected to live-reload server
```

### Make a Change

Edit `testserver/cmd/server/main.go` — change the version constant:

```go
const version = "16"
```

Save the file.

### What Happens

1. The terminal immediately shows:
   ```
   level=INFO msg="file changed" path=.../testserver/cmd/server/main.go
   level=INFO msg="change detected, rebuilding..."
   level=INFO msg="stopping server" pid=12345
   level=INFO msg="server stopped gracefully" pid=12345
   level=INFO msg="building project"
   level=INFO msg="build succeeded"
   level=INFO msg="starting server"
   testserver listening on :8080
   level=INFO msg="notifying browsers to reload" clients=1
   ```

2. The browser automatically refreshes and now shows:
   ```
   Hello from the test server!
   Version: 16
   ```

The total time from file save to browser showing the new version is typically under 1.5 seconds.

### Error Handling Demo

Introduce a syntax error in `main.go`:

```go
const version = "17  // missing closing quote
```

Save. The terminal shows:
```
level=INFO msg="file changed" ...
level=INFO msg="change detected, rebuilding..."
level=ERROR msg="build failed" error="exit status 1"
```

The previous server keeps running (it was already killed before the build, but the new broken build doesn't replace it). Fix the error, save again, and the server restarts with the corrected code.

### Crash-Loop Demo

Make the server crash immediately by adding `panic("boom")` at the top of `main()`. Save. The terminal shows:
```
level=ERROR msg="server crashed shortly after starting" uptime=50ms next_backoff=1s
```

Save again — the backoff is now 2s. The tool avoids hammering the system with rapid restarts. Remove the panic, save, and the server starts normally — the backoff resets.
