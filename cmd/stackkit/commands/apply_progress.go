package commands

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/kombifyio/stackkits/internal/runtimeexecutordispatch"
)

const defaultApplyProgressHeartbeat = 15 * time.Second

// applyOwnerLivenessEvery is how often Apply reports that a runtime owner is
// still executing. A consumer that stops hearing from a running Apply for a
// few of these intervals has a stuck process, not a slow one.
var applyOwnerLivenessEvery = runtimeexecutordispatch.DefaultOwnerLivenessInterval

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

// applyOwnerProgressContext makes Apply report each runtime owner through the
// rollout sinks. The human heartbeat above stays silent in machine mode; this
// is its machine-readable counterpart and writes only to the rollout sinks
// (--progress-jsonl, the run recorder and the deploy log), never to stdout or
// stderr, so the single --json result document is untouched.
func applyOwnerProgressContext(ctx context.Context) context.Context {
	if !rolloutProgressSinkActive() {
		return ctx
	}
	return runtimeexecutordispatch.WithOwnerObserver(ctx, recordApplyOwnerEvent, applyOwnerLivenessEvery)
}

// recordApplyOwnerEvent maps one owner observation to a stackkit.rollout-event/v1
// event. Only identities, positions and timings leave the process here. The
// cause of a failure is reduced to its failure class and the journal failure
// code: the error text may carry command lines, paths or secrets and stays in
// the Apply result.
func recordApplyOwnerEvent(event runtimeexecutordispatch.OwnerEvent) {
	attributes := map[string]string{
		"scope":    event.Scope,
		"owner":    event.Label,
		"position": strconv.Itoa(event.Position),
		"total":    strconv.Itoa(event.Total),
		"targets":  strconv.Itoa(event.Targets),
	}
	if event.Module != "" {
		attributes["module"] = event.Module
	}
	if event.Workload != "" {
		attributes["workload"] = event.Workload
	}
	if event.Status != runtimeexecutordispatch.OwnerStarted && !event.Replayed {
		attributes["elapsed_ms"] = strconv.FormatInt(event.Elapsed.Milliseconds(), 10)
	}
	if event.Replayed {
		attributes["replayed"] = "true"
	}
	// The message alone must tell a consumer that only renders messages (a
	// live log) which owner it is about, where it stands, and how long it ran.
	subject := "runtime owner"
	if event.Scope == runtimeexecutordispatch.ScopeChannel {
		subject = "execution channel"
	}
	outcome := event.Status
	switch {
	case event.Replayed:
		outcome = "already applied"
	case event.Status == runtimeexecutordispatch.OwnerRunning:
		outcome = "still running (" + event.Elapsed.Round(time.Second).String() + ")"
	case event.Status != runtimeexecutordispatch.OwnerStarted:
		outcome += " (" + event.Elapsed.Round(time.Second).String() + ")"
	}
	recorded := rollout.Event{
		Phase:  "apply." + event.Scope,
		Status: event.Status,
		Message: subject + " " + strconv.Itoa(event.Position) + "/" + strconv.Itoa(event.Total) + " " +
			event.Label + ": " + outcome,
		Attributes: attributes,
	}
	if event.Status == runtimeexecutordispatch.OwnerFailed {
		recorded.FailureClass = "unknown_failure"
		if event.Err != nil {
			recorded.FailureClass = rollout.ClassifyFailure(event.Err.Error())
		}
		attributes["failure_code"] = string(event.FailureCode)
		if event.Degraded {
			attributes["degraded"] = "true"
		}
	}
	rolloutRecordEvent(recorded)
}
