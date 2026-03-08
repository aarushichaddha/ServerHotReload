package livereload

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// Server is an SSE-based live-reload server. Browsers connect via EventSource
// and receive a "reload" event whenever the application server is restarted.
type Server struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

// New creates a new live-reload Server.
func New() *Server {
	return &Server{
		clients: make(map[chan struct{}]struct{}),
	}
}

// Start begins listening on the given address (e.g. ":35729").
// It serves two endpoints:
//   - GET /livereload  — SSE stream that sends "reload" events
//   - GET /livereload.js — JavaScript snippet browsers can include
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/livereload", s.handleSSE)
	mux.HandleFunc("/livereload.js", s.handleJS)

	slog.Info("livereload server starting", "addr", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("livereload server failed", "error", err)
		}
	}()
	return nil
}

// Reload notifies all connected browsers to refresh the page.
func (s *Server) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Info("notifying browsers to reload", "clients", len(s.clients))
	for ch := range s.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// handleSSE serves the Server-Sent Events stream.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	// Send an initial "connected" event so the browser knows it's working.
	fmt.Fprintf(w, "event: connected\ndata: ok\n\n")
	flusher.Flush()

	// Block until we get a reload signal or the client disconnects.
	for {
		select {
		case <-ch:
			fmt.Fprintf(w, "event: reload\ndata: reload\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleJS serves a small JavaScript snippet that pages can include
// to get auto-reload behavior.
func (s *Server) handleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// The script connects to the SSE endpoint and reloads on "reload" events.
	// On connection loss (server restarting), it retries automatically.
	fmt.Fprint(w, jsSnippet)
}

const jsSnippet = `(function() {
  var source;
  var reconnectInterval = 500;

  function connect() {
    source = new EventSource('http://' + location.hostname + ':35729/livereload');

    source.addEventListener('connected', function() {
      console.log('[hotreload] connected to live-reload server');
    });

    source.addEventListener('reload', function() {
      console.log('[hotreload] reloading page...');
      location.reload();
    });

    source.onerror = function() {
      source.close();
      setTimeout(connect, reconnectInterval);
    };
  }

  connect();
})();
`

// ScriptTag returns an HTML script tag that loads the livereload JS from
// the given livereload server port.
func ScriptTag(port int) string {
	return fmt.Sprintf(`<script src="http://localhost:%d/livereload.js"></script>`, port)
}
