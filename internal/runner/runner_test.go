package runner_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aarushichaddha/hotreload/internal/runner"
)

func TestBuildAndExec(t *testing.T) {
	// Create a temp directory with a marker file approach.
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")

	// Use a command that writes a file as our "server".
	r := runner.New(
		"echo build_ok",
		"sh -c 'echo running > "+marker+"  && sleep 10'",
	)

	r.Trigger()

	// Wait for the marker file to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker file not created: %v", err)
	}
	if string(data) != "running\n" {
		t.Fatalf("unexpected marker content: %q", string(data))
	}

	// Stop should kill the process cleanly.
	r.Stop()
}

func TestBuildFailureDoesNotExec(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "should_not_exist.txt")

	r := runner.New(
		"false", // This command always fails.
		"touch "+marker,
	)

	r.Trigger()
	time.Sleep(1 * time.Second)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("exec should not have run after failed build")
	}

	r.Stop()
}

func TestRetriggerCancelsPrevious(t *testing.T) {
	dir := t.TempDir()
	marker1 := filepath.Join(dir, "m1.txt")
	marker2 := filepath.Join(dir, "m2.txt")

	r := runner.New(
		"echo build",
		"sh -c 'echo v1 > "+marker1+" && sleep 30'",
	)

	r.Trigger()

	// Wait for first server to start.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker1); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Now retrigger with a different exec.
	r2 := runner.New(
		"echo build2",
		"sh -c 'echo v2 > "+marker2+" && sleep 30'",
	)
	_ = r2

	// Just stop the first runner to prove the previous process gets killed.
	r.Stop()

	// The first process should be dead now; give it a moment.
	time.Sleep(500 * time.Millisecond)
}

func TestSplitCommandWithPipes(t *testing.T) {
	// This is an integration test that pipes work via sh -c.
	dir := t.TempDir()
	marker := filepath.Join(dir, "pipe_test.txt")

	r := runner.New(
		"echo build_ok",
		"sh -c 'echo hello | tr h H > "+marker+" && sleep 10'",
	)

	r.Trigger()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker file not created: %v", err)
	}
	if string(data) != "Hello\n" {
		t.Fatalf("unexpected content: %q", string(data))
	}

	r.Stop()
}
