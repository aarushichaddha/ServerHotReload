# ServerHotReload

A CLI tool that watches a Go project folder for code changes and automatically rebuilds and restarts the server.

## Features

- **File watching** — Recursively watches directories for `.go` file changes using `fsnotify`
- **Auto rebuild & restart** — Rebuilds and restarts the server within ~1 second of a file save
- **Debouncing** — Coalesces rapid file events (e.g., editor save bursts) into a single rebuild
- **Build cancellation** — If a new change arrives during a build, the previous build is discarded
- **Process group kill** — Kills the server and all its child processes using SIGTERM, escalating to SIGKILL after 3 seconds
- **Crash loop detection** — Exponential backoff (1s → 2s → 4s → ... → 30s max) if the server crashes immediately after starting
- **Dynamic directory watching** — Detects and watches new directories created at runtime; handles deleted directories gracefully
- **File filtering** — Ignores `.git/`, `node_modules/`, `vendor/`, `bin/`, build artifacts, and temporary editor files (`.swp`, `.swo`, `~`, etc.)
- **Live reload** — Built-in SSE server that automatically refreshes the browser when the server restarts
- **Real-time log streaming** — Server stdout/stderr streams directly to the terminal without buffering

## Prerequisites

### Install Go

#### macOS

**Option 1 -- Homebrew (recommended):**
```bash
brew install go
```

**Option 2 -- Official installer:**
1. Download the `.pkg` installer from https://go.dev/dl/
2. Open the downloaded file and follow the prompts
3. Go will be installed to `/usr/local/go`

Verify the installation:
```bash
go version
```

#### Windows

**Option 1 -- Winget:**
```powershell
winget install GoLang.Go
```

**Option 2 -- Official installer:**
1. Download the `.msi` installer from https://go.dev/dl/
2. Run the installer and follow the prompts
3. Go will be installed to `C:\Program Files\Go`

Verify the installation (open a new terminal after installing):
```powershell
go version
```

### Install Make (optional, for Makefile targets)

#### macOS

Make is included with the Xcode Command Line Tools:
```bash
xcode-select --install
```

#### Windows

Make is not included by default. You can either:
- Install via [Chocolatey](https://chocolatey.org/): `choco install make`
- Or skip the Makefile and use the `go build` commands directly (shown below)

## Libraries Used

| Library | Version | Purpose |
|---------|---------|---------|
| [`github.com/fsnotify/fsnotify`](https://github.com/fsnotify/fsnotify) | v1.9.0 | Cross-platform file system event notifications |
| `golang.org/x/sys` | v0.13.0 | (indirect) Low-level OS primitives used by fsnotify |

All other functionality uses the **Go standard library**:

| Stdlib Package | Purpose |
|----------------|---------|
| `log/slog` | Structured logging |
| `os/exec` | Running build and server commands |
| `syscall` | Process group management (SIGTERM/SIGKILL) |
| `context` | Build cancellation |
| `net/http` | Live reload SSE server |

Go modules will download these automatically during the build step -- no manual dependency installation is needed.

## Installation

### macOS / Linux

```bash
# Clone the repository
git clone https://github.com/aarushichaddha/hotreload.git
cd hotreload

# Download dependencies and build
go build -o bin/hotreload ./cmd/hotreload
```

Or use the Makefile:

```bash
make build
```

### Windows

```powershell
# Clone the repository
git clone https://github.com/aarushichaddha/hotreload.git
cd hotreload

# Download dependencies and build
go build -o bin\hotreload.exe .\cmd\hotreload
```

> **Note:** Makefile targets (`make run`, `make test`, etc.) require `make` to be installed on Windows. Alternatively, run the underlying Go commands directly.

## Usage

```bash
hotreload --root <project-folder> --build "<build-command>" --exec "<run-command>"
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--root` | `.` | Directory to watch for file changes (recursive) |
| `--build` | *(required)* | Command to build the project |
| `--exec` | *(required)* | Command to run the built server |
| `--verbose` | `false` | Enable debug-level logging |
| `--livereload` | `35729` | Port for the live-reload SSE server (0 to disable) |

### Example

```bash
hotreload \
  --root ./myproject \
  --build "go build -o ./bin/server ./cmd/server" \
  --exec "./bin/server"
```

## Demo

A sample test server is included in `testserver/`.

```bash
# Build hotreload and run it watching the test server
make run
```

Then open http://localhost:8080 in your browser. Edit `testserver/cmd/server/main.go` (e.g., change the `version` constant), save, and the browser will auto-refresh with the new version.

## Architecture

```
cmd/hotreload/main.go          CLI entry point, flag parsing, event loop
internal/
├── watcher/watcher.go          Recursive fsnotify watcher with filtering
├── debouncer/debouncer.go      Coalesces rapid events into single triggers
├── runner/runner.go            Build + exec lifecycle, process management
└── livereload/livereload.go    SSE server for browser auto-refresh
testserver/cmd/server/main.go   Sample HTTP server for demonstration
```

### Event Flow

```
File saved
    → fsnotify event
    → watcher filters (ignores .git, temp files, etc.)
    → debouncer (200ms quiet period)
    → runner.Trigger()
        → cancel previous build (if any)
        → kill previous server (SIGTERM → SIGKILL)
        → run build command
        → run exec command
        → notify livereload SSE clients
            → browser calls location.reload()
```

## Special Live Reload Which I have added

The tool runs an SSE (Server-Sent Events) server on port 35729 by default. To enable browser auto-refresh, include this script tag in your HTML:

```html
<script src="http://localhost:35729/livereload.js"></script>
```

The script connects to the SSE endpoint and calls `location.reload()` when the server restarts. It automatically reconnects if the connection drops.

Disable with `--livereload 0`.

## Running Tests

```bash
make test
```

Tests cover:
- **Debouncer** — single signal, rapid signal coalescing, timer reset, no-signal timeout
- **Runner** — build+exec lifecycle, build failure handling, retrigger cancellation, shell metacharacter commands
- **Watcher** — file change detection, new file detection, `.git` directory filtering, dynamic directory watching

All tests run with the `-race` detector enabled.

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build hotreload and testserver binaries |
| `make run` | Build hotreload and run it watching the testserver |
| `make test` | Run all tests with race detector |
| `make clean` | Remove build artifacts |

## Constraints

- Does **not** use existing hot-reload frameworks (air, realize, reflex)
- Uses `fsnotify` only as the filesystem event source
- Uses `log/slog` from the Go standard library for structured logging
