// Package engine runs backups and restores and reports their progress.
package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/run"
)

// StepState is the lifecycle of one step.
type StepState int

const (
	Pending StepState = iota
	Running
	Done
	Failed
	Skipped
)

// Step is one visible unit of work.
type Step struct {
	p      *Progress
	Title  string
	Detail string
	Note   string
	Weight int64 // expected bytes (0 = counts as a small fixed amount)
	Done   int64
	State  StepState
	Err    error
	Start  time.Time
	End    time.Time
}

// Progress is shared between the engine (writer) and the UI (reader).
type Progress struct {
	mu       sync.Mutex
	Title    string
	steps    []*Step
	warnings []string
	started  time.Time
	// Output reports extra live numbers (e.g. compressed size so far).
	Output   func() int64
	finished bool
	err      error
}

// NewProgress creates an empty progress tracker.
func NewProgress(title string) *Progress {
	return &Progress{Title: title, started: time.Now()}
}

// Add appends a step.
func (p *Progress) Add(title string, weight int64) *Step {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := &Step{p: p, Title: title, Weight: weight}
	p.steps = append(p.steps, s)
	return s
}

// Begin marks the step as running.
func (s *Step) Begin(detail string) {
	s.p.mu.Lock()
	s.State, s.Detail, s.Start = Running, detail, time.Now()
	s.p.mu.Unlock()
	run.Logf("step start: %s %s", s.Title, detail)
}

// SetDetail updates the detail line of a running step.
func (s *Step) SetDetail(detail string) {
	s.p.mu.Lock()
	s.Detail = detail
	s.p.mu.Unlock()
}

// SetWeight changes the expected size (e.g. once known).
func (s *Step) SetWeight(w int64) {
	s.p.mu.Lock()
	s.Weight = w
	s.p.mu.Unlock()
}

// Advance adds processed bytes.
func (s *Step) Advance(n int64) {
	s.p.mu.Lock()
	s.Done += n
	s.p.mu.Unlock()
}

// Finish marks the step as done with a short result note.
func (s *Step) Finish(note string) {
	s.p.mu.Lock()
	s.State, s.Note, s.End = Done, note, time.Now()
	if s.Weight > 0 && s.Done < s.Weight {
		s.Done = s.Weight
	}
	s.p.mu.Unlock()
	run.Logf("step done: %s %s", s.Title, note)
}

// Fail marks the step as failed.
func (s *Step) Fail(err error) {
	s.p.mu.Lock()
	s.State, s.Err, s.End = Failed, err, time.Now()
	s.p.mu.Unlock()
	run.Logf("step FAILED: %s: %v", s.Title, err)
}

// SkipStep marks the step as skipped.
func (s *Step) SkipStep(note string) {
	s.p.mu.Lock()
	s.State, s.Note, s.End = Skipped, note, time.Now()
	s.p.mu.Unlock()
}

// Warn records a warning shown in the summary.
func (p *Progress) Warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	p.mu.Lock()
	p.warnings = append(p.warnings, msg)
	p.mu.Unlock()
	run.Logf("warning: %s", msg)
}

// End marks the whole operation finished (err nil = success).
func (p *Progress) End(err error) {
	p.mu.Lock()
	p.finished, p.err = true, err
	p.mu.Unlock()
}

// StepView is an immutable copy of a step for rendering.
type StepView struct {
	Title, Detail, Note string
	Weight, Done        int64
	State               StepState
	Err                 error
	Elapsed             time.Duration
}

// View is an immutable snapshot of the progress.
type View struct {
	Title      string
	Steps      []StepView
	Warnings   []string
	Elapsed    time.Duration
	TotalBytes int64
	DoneBytes  int64
	Output     int64
	Finished   bool
	Err        error
}

// Fraction returns overall completion in [0,1].
func (v View) Fraction() float64 {
	var total, done float64
	for _, s := range v.Steps {
		w := float64(s.Weight)
		if w <= 0 {
			w = 1 << 20 // small steps count as 1 MiB of work
		}
		total += w
		switch s.State {
		case Done, Skipped:
			done += w
		case Running:
			d := float64(s.Done)
			if s.Weight <= 0 {
				d = 0
			}
			if d > w {
				d = w
			}
			done += d
		}
	}
	if total == 0 {
		return 0
	}
	return done / total
}

// Snapshot copies the current state.
func (p *Progress) Snapshot() View {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := View{Title: p.Title, Elapsed: time.Since(p.started), Finished: p.finished, Err: p.err}
	v.Warnings = append(v.Warnings, p.warnings...)
	for _, s := range p.steps {
		sv := StepView{Title: s.Title, Detail: s.Detail, Note: s.Note, Weight: s.Weight, Done: s.Done, State: s.State, Err: s.Err}
		if !s.Start.IsZero() {
			end := s.End
			if end.IsZero() {
				end = time.Now()
			}
			sv.Elapsed = end.Sub(s.Start)
		}
		v.Steps = append(v.Steps, sv)
		v.TotalBytes += s.Weight
		if s.Done > s.Weight && s.Weight > 0 {
			v.DoneBytes += s.Weight
		} else {
			v.DoneBytes += s.Done
		}
	}
	if p.Output != nil {
		v.Output = p.Output()
	}
	return v
}
