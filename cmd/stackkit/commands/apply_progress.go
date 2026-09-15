package commands

import (
	"sync"
	"time"
)

const defaultApplyProgressHeartbeat = 15 * time.Second

func startApplyProgressHeartbeat(interval time.Duration) func() {
	if humanOutputSuppressed() || quiet {
		return func() {}
	}
	if interval <= 0 {
		interval = defaultApplyProgressHeartbeat
	}
	printInfo("Installing services. Image download and health waits can take several minutes.")
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() { close(done) })
	}
	started := time.Now()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				printInfo("Still installing services (%s elapsed). Waiting on image download or container health.", now.Sub(started).Round(time.Second))
			}
		}
	}()
	return stop
}
