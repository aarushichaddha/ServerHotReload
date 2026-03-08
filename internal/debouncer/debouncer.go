package debouncer

import (
	"sync"
	"time"
)

// Debouncer coalesces rapid events into a single trigger.
// After each incoming signal, it waits for `interval` of quiet time
// before firing. If new signals arrive during the wait, the timer resets.
type Debouncer struct {
	interval time.Duration
	mu       sync.Mutex
	timer    *time.Timer
	output   chan struct{}
}

// New creates a Debouncer with the given quiet interval.
func New(interval time.Duration) *Debouncer {
	return &Debouncer{
		interval: interval,
		output:   make(chan struct{}, 1),
	}
}

// Signal notifies the debouncer that an event occurred.
// The debouncer resets its internal timer on every call.
func (d *Debouncer) Signal() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.interval, func() {
		// Non-blocking send; if a signal is already pending, skip.
		select {
		case d.output <- struct{}{}:
		default:
		}
	})
}

// Events returns a read-only channel that fires after the quiet period.
func (d *Debouncer) Events() <-chan struct{} {
	return d.output
}
