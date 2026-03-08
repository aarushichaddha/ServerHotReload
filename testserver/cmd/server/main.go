package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

const version = "15"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Test Server</title></head>
<body>
  <h1>Hello from the test server!</h1>
  <p>Version: %s</p>
  <script src="http://localhost:35729/livereload.js"></script>
</body>
</html>
`, version)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK\n")
	})

	addr := ":" + port
	log.Printf("testserver listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
