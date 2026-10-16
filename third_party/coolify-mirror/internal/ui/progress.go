package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/engine"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type sample struct {
	at   time.Time
	done int64
}

type progressModel struct {
	title      string
	pr         *engine.Progress
	cancel     context.CancelFunc
	spin       spinner.Model
	bar        progress.Model
	width      int
	view       engine.View
	samples    []sample
	cancelling bool
	final      bool
	outLabel   string
}

func newProgressModel(title string, pr *engine.Progress, cancel context.CancelFunc) *progressModel {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	bar := progress.New(progress.WithGradient("#7C3AED", "#22C55E"), progress.WithoutPercentage())
	return &progressModel{title: title, pr: pr, cancel: cancel, spin: sp, bar: bar, width: 80}
}

func (m *progressModel) Init() tea.Cmd { return tea.Batch(m.spin.Tick, tick()) }

func (m *progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" && !m.cancelling {
			m.cancelling = true
			m.cancel()
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tickMsg:
		m.view = m.pr.Snapshot()
		now := time.Time(msg)
		m.samples = append(m.samples, sample{now, m.view.DoneBytes})
		for len(m.samples) > 2 && now.Sub(m.samples[0].at) > 8*time.Second {
			m.samples = m.samples[1:]
		}
		if m.view.Finished {
			m.final = true
			return m, tea.Quit
		}
		return m, tick()
	}
	return m, nil
}

func (m *progressModel) rate() float64 {
	if len(m.samples) < 2 {
		return 0
	}
	a, b := m.samples[0], m.samples[len(m.samples)-1]
	dt := b.at.Sub(a.at).Seconds()
	if dt < 0.5 {
		return 0
	}
	return float64(b.done-a.done) / dt
}

func truncate(s string, n int) string {
	if n <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (m *progressModel) View() string {
	v := m.view
	w := m.width
	if w <= 0 {
		w = 80
	}
	if w > 110 {
		w = 110
	}
	var b strings.Builder
	head := sTitle.Render("◆ " + m.title)
	if m.cancelling && !v.Finished {
		head += "  " + sWarn.Render("cancelling… (paused containers are resumed)")
	}
	b.WriteString(head + "\n")

	// Show recent finished steps, all running ones and a few upcoming ones.
	steps := v.Steps
	var doneIdx, runIdx, pendIdx []int
	for i, s := range steps {
		switch s.State {
		case engine.Done, engine.Skipped, engine.Failed:
			doneIdx = append(doneIdx, i)
		case engine.Running:
			runIdx = append(runIdx, i)
		default:
			pendIdx = append(pendIdx, i)
		}
	}
	show := map[int]bool{}
	hiddenDone := 0
	if len(steps) <= 14 || (m.final && len(steps) <= 30) {
		for i := range steps {
			show[i] = true
		}
	} else {
		keep := 4
		for k, i := range doneIdx {
			if k >= len(doneIdx)-keep || steps[i].State == engine.Failed {
				show[i] = true
			} else {
				hiddenDone++
			}
		}
		for _, i := range runIdx {
			show[i] = true
		}
		for k, i := range pendIdx {
			if k < 5 {
				show[i] = true
			}
		}
	}
	if hiddenDone > 0 {
		b.WriteString(sOK.Render("  ✓ ") + sMuted.Render(fmt.Sprintf("%d earlier steps done", hiddenDone)) + "\n")
	}
	// The title column is as wide as the longest title shown (at most w-34),
	// so the notes on the right get the rest instead of being cut short.
	longest := 0
	for i, s := range steps {
		if show[i] {
			longest = max(longest, lipgloss.Width(s.Title))
		}
	}
	titleW := min(longest, w-34)
	if titleW < 24 {
		titleW = 24
	}
	shownPending := 0
	for i, s := range steps {
		if !show[i] {
			continue
		}
		var icon, right string
		title := truncate(s.Title, titleW)
		switch s.State {
		case engine.Done:
			icon = sOK.Render("✓")
			right = sMuted.Render(s.Note)
		case engine.Skipped:
			icon = sMuted.Render("–")
			right = sMuted.Render(s.Note)
			title = sMuted.Render(title)
		case engine.Failed:
			icon = sErr.Render("✗")
			right = sErr.Render(fmt.Sprint(s.Err))
		case engine.Running:
			icon = m.spin.View()
			title = sBold.Render(title)
			if s.Weight > 0 {
				right = fmt.Sprintf("%s / %s", engine.HumanBytes(min64(s.Done, s.Weight)), engine.HumanBytes(s.Weight))
			}
			if s.Detail != "" {
				right = strings.TrimSpace(right + "  " + sMuted.Render(s.Detail))
			}
		default:
			icon = sMuted.Render("○")
			title = sMuted.Render(title)
			shownPending++
		}
		line := fmt.Sprintf("  %s %s", icon, title)
		if right != "" {
			pad := titleW - lipgloss.Width(title) + 2
			if pad < 2 {
				pad = 2
			}
			line += strings.Repeat(" ", pad) + truncateStyled(right, w-titleW-6)
		}
		b.WriteString(line + "\n")
	}
	if more := len(pendIdx) - shownPending; more > 0 && !m.final {
		b.WriteString(sMuted.Render(fmt.Sprintf("    … %d more", more)) + "\n")
	}

	frac := v.Fraction()
	if v.Finished && v.Err == nil {
		frac = 1
	}
	m.bar.Width = w - 12
	if m.bar.Width < 20 {
		m.bar.Width = 20
	}
	b.WriteString("\n  " + m.bar.ViewAs(frac) + fmt.Sprintf(" %3.0f%%\n", frac*100))
	info := []string{}
	if v.TotalBytes > 0 {
		info = append(info, fmt.Sprintf("%s of %s", engine.HumanBytes(min64(v.DoneBytes, v.TotalBytes)), engine.HumanBytes(v.TotalBytes)))
	}
	if r := m.rate(); r > 0 && !v.Finished {
		info = append(info, engine.HumanBytes(int64(r))+"/s")
		if left := v.TotalBytes - v.DoneBytes; left > 0 {
			info = append(info, "about "+engine.HumanDuration(time.Duration(float64(left)/r*float64(time.Second)))+" left")
		}
	}
	info = append(info, "elapsed "+engine.HumanDuration(v.Elapsed))
	if v.Output > 0 {
		info = append(info, "file "+engine.HumanBytes(v.Output))
	}
	b.WriteString("  " + sMuted.Render(strings.Join(info, " · ")) + "\n")
	if len(v.Warnings) > 0 {
		last := v.Warnings[len(v.Warnings)-1]
		b.WriteString("  " + sWarn.Render(fmt.Sprintf("! %d warning(s) - latest: %s", len(v.Warnings), truncate(last, w-30))) + "\n")
	}
	if !v.Finished && !m.cancelling {
		b.WriteString(sMuted.Render("  ctrl+c to cancel") + "\n")
	}
	return b.String()
}

func truncateStyled(s string, n int) string {
	if n < 8 || lipgloss.Width(s) <= n {
		return s
	}
	return truncate(stripANSI(s), n)
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// runWithProgress runs work in the background and shows its progress until it
// ends. It returns work's error.
func runWithProgress(ctx context.Context, title string, pr *engine.Progress, work func(ctx context.Context) error) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		err := work(wctx)
		pr.End(err)
		errc <- err
	}()
	m := newProgressModel(title, pr, cancel)
	if _, err := tea.NewProgram(m, tea.WithContext(ctx)).Run(); err != nil {
		cancel()
	}
	err := <-errc
	if v := pr.Snapshot(); len(v.Warnings) > 0 {
		max := 12
		for i, w := range v.Warnings {
			if i == max {
				fmt.Println("  " + sWarn.Render(fmt.Sprintf("! … and %d more (see %s)", len(v.Warnings)-max, LogPath)))
				break
			}
			fmt.Println("  " + sWarn.Render("! "+w))
		}
	}
	return err
}
