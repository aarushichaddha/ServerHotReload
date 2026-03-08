.PHONY: build build-hotreload build-testserver run clean test

# Build everything
build: build-hotreload build-testserver

# Build the hotreload CLI tool
build-hotreload:
	go build -o bin/hotreload ./cmd/hotreload

# Build the sample test server
build-testserver:
	go build -o bin/server ./testserver/cmd/server

# Run the demo: hotreload watching the testserver
run: build-hotreload
	./bin/hotreload \
		--root ./testserver \
		--build "go build -o ./bin/server ./testserver/cmd/server" \
		--exec "./bin/server"

# Run with verbose logging
run-verbose: build-hotreload
	./bin/hotreload \
		--root ./testserver \
		--build "go build -o ./bin/server ./testserver/cmd/server" \
		--exec "./bin/server" \
		--verbose

# Run tests
test:
	go test -v -race ./internal/...

# Clean build artifacts
clean:
	rm -rf bin/
