package offlinerecovery

import "time"

var processClockAnchor = time.Now()

// SystemClock returns the wall clock and a monotonic reading from this process.
// The monotonic anchor is intentionally process-local: a restart cannot inherit
// trusted remaining authority and must admit a newly signed authority.
func SystemClock() ClockReading {
	return ClockReading{Wall: time.Now().UTC(), Monotonic: time.Since(processClockAnchor)}
}
