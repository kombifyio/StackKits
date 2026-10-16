package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5B3CC4", Dark: "#A78BFA"}
	colOK     = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#4ADE80"}
	colErr    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F87171"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#FBBF24"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	colText   = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#E6EDF3"}

	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	sBold   = lipgloss.NewStyle().Bold(true).Foreground(colText)
	sMuted  = lipgloss.NewStyle().Foreground(colMuted)
	sOK     = lipgloss.NewStyle().Foreground(colOK)
	sErr    = lipgloss.NewStyle().Foreground(colErr)
	sWarn   = lipgloss.NewStyle().Foreground(colWarn)
	sAccent = lipgloss.NewStyle().Foreground(colAccent)
	sBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)
	sErrBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colErr).Padding(0, 1)
	sLink   = lipgloss.NewStyle().Bold(true).Foreground(colOK)
)

// boxed renders s in a bordered box that never exceeds the terminal width
// (long lines wrap inside the box instead of breaking the border).
func boxed(st lipgloss.Style, s string) string {
	w := termWidth() - 1
	widest := 0
	for _, line := range strings.Split(s, "\n") {
		if lw := lipgloss.Width(line); lw > widest {
			widest = lw
		}
	}
	if widest+4 > w {
		st = st.Width(w - 2)
	}
	return st.Render(s)
}

// keys: esc goes back (ends the form like ctrl+c), as the screens say.
func keys() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "back"))
	km.Select.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))
	km.Select.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", "choose"))
	// The single-choice lists are short; no search there, so esc never
	// has to mean "leave the search".
	km.Select.Filter = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search"), key.WithDisabled())
	return km
}

// listKeys: the keys of a multi-select list, with the help line naming space
// (what the descriptions say) and enter as continue.
func listKeys() *huh.KeyMap {
	km := keys()
	km.MultiSelect.Toggle = key.NewBinding(key.WithKeys(" ", "x"), key.WithHelp("space", "tick"))
	km.MultiSelect.Submit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue"))
	km.MultiSelect.Next = key.NewBinding(key.WithKeys("enter", "tab"), key.WithHelp("enter", "continue"))
	km.MultiSelect.Filter = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search"))
	return km
}

func theme() *huh.Theme {
	t := huh.ThemeCharm()
	t.Focused.Base = t.Focused.Base.BorderForeground(colAccent)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(colAccent).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(colMuted)
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(colAccent).Bold(true).SetString("❯ ")
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(colAccent).Bold(true).SetString("❯ ")
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(colAccent)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(colAccent)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(colOK).SetString("[✓] ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(colMuted).SetString("[ ] ")
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colOK)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("#0B1020")).Background(colAccent).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colAccent)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colAccent)
	t.Help.ShortKey = t.Help.ShortKey.Foreground(colAccent)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(colMuted)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(colMuted)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}

// state renders how much of a project or resource runs, coloured.
func state(running, total int) string {
	switch {
	case total > 0 && running == total:
		return sOK.Render("● running")
	case running > 0:
		return sWarn.Render(fmt.Sprintf("◐ %d of %d running", running, total))
	}
	return sMuted.Render("○ stopped")
}

// rule is a section line: "── Title ──────".
func rule(title string) string {
	w := termWidth() - 4
	if w > 72 {
		w = 72
	}
	line := "── " + title + " "
	if n := w - lipgloss.Width(line); n > 0 {
		line += strings.Repeat("─", n)
	}
	return "  " + sAccent.Render(line)
}
