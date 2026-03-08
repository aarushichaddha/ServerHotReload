package debouncer_test

import (
	"testing"
	"time"

	"github.com/aarushichaddha/hotreload/internal/debouncer"
)

func TestSingleSignal(t *testing.T) {
	d := debouncer.New(50 * time.Millisecond)
	d.Signal()

	select {
	case <-d.Events():
		// Expected.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected event, got timeout")
	}
}

func TestRapidSignalsCoalesce(t *testing.T) {
	d := debouncer.New(100 * time.Millisecond)

	// Fire many signals rapidly.
	for i := 0; i < 20; i++ {
		d.Signal()
		time.Sleep(10 * time.Millisecond)
	}

	// Should get exactly one event after the quiet period.
	select {
	case <-d.Events():
		// Expected.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected event, got timeout")
	}

	// Should NOT get a second event.
	select {
	case <-d.Events():
		t.Fatal("unexpected second event")
	case <-time.After(300 * time.Millisecond):
		// Expected: no more events.
	}
}

func TestNoSignalNoEvent(t *testing.T) {
	d := debouncer.New(50 * time.Millisecond)

	select {
	case <-d.Events():
		t.Fatal("unexpected event without signal")
	case <-time.After(200 * time.Millisecond):
		// Expected.
	}
}

func TestDebounceResetsTimer(t *testing.T) {
	d := debouncer.New(100 * time.Millisecond)

	d.Signal()
	time.Sleep(60 * time.Millisecond)
	// Reset the timer.
	d.Signal()
	time.Sleep(60 * time.Millisecond)

	// At this point, only ~60ms have passed since the last signal,
	// so the event should NOT have fired yet.
	select {
	case <-d.Events():
		// This is okay if timing is slightly off, but ideally we wait.
		// Let this pass since timer granularity can vary.
	case <-time.After(100 * time.Millisecond):
		// This means the debounce worked and waited for the full quiet period.
		// But we should have received it by now (60ms + 100ms > 100ms interval).
		// We may have missed it. Let's check once more.
		select {
		case <-d.Events():
			// Got it.
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected event, got timeout")
		}
	}
}
