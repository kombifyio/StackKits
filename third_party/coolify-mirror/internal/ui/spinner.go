package ui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type spinDone struct{}

type spinModel struct {
	title string
	spin  spinner.Model
	done  bool
}

func (m *spinModel) Init() tea.Cmd { return m.spin.Tick }

func (m *spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinDone:
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Interrupt
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *spinModel) View() string {
	if m.done {
		return ""
	}
	return "  " + m.spin.View() + " " + m.title + "\n"
}

// withSpinner runs fn while showing a spinner line, then prints ✓ or ✗.
func withSpinner[T any](ctx context.Context, title string, fn func(context.Context) (T, error)) (T, error) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var res T
	var err error
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sAccent
	m := &spinModel{title: title, spin: sp}
	p := tea.NewProgram(m, tea.WithContext(ctx))
	done := make(chan struct{})
	go func() {
		res, err = fn(wctx)
		close(done)
		p.Send(spinDone{})
	}()
	if _, perr := p.Run(); perr != nil {
		cancel()
		<-done
		if err == nil {
			err = context.Canceled
		}
	}
	<-done
	if err != nil {
		fmt.Println("  " + sErr.Render("✗ ") + title + ": " + sErr.Render(err.Error()))
	} else {
		fmt.Println("  " + sOK.Render("✓ ") + title)
	}
	return res, err
}
