package commands

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// prompter wraps interactive terminal prompts.
// All methods return ("", ErrNonInteractive) when non-interactive mode is set.
type prompter struct {
	scanner *bufio.Scanner
}

func newPrompter() *prompter {
	return &prompter{scanner: bufio.NewScanner(os.Stdin)}
}

// choice represents a selectable option.
type choice struct {
	Key         string // internal value returned on selection
	Display     string // shown to user
	Description string // optional description line
	IsDefault   bool
}

// selectOne presents a numbered list and returns the selected choice key.
func (p *prompter) selectOne(heading string, choices []choice) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no options available")
	}

	fmt.Println()
	fmt.Printf("  %s\n\n", bold(heading))

	defaultIdx := -1
	for i, c := range choices {
		marker := " "
		if c.IsDefault {
			marker = "*"
			defaultIdx = i
		}
		if c.Description != "" {
			fmt.Printf("  %s %s  %s  %s\n", marker, cyan(fmt.Sprintf("[%d]", i+1)), c.Display, dim(c.Description))
		} else {
			fmt.Printf("  %s %s  %s\n", marker, cyan(fmt.Sprintf("[%d]", i+1)), c.Display)
		}
	}

	defaultHint := ""
	if defaultIdx >= 0 {
		defaultHint = fmt.Sprintf(" [%d]", defaultIdx+1)
	}

	fmt.Printf("\n  Choose%s: ", defaultHint)

	if !p.scanner.Scan() {
		return "", fmt.Errorf("input canceled")
	}

	input := strings.TrimSpace(p.scanner.Text())

	// Empty input → default
	if input == "" && defaultIdx >= 0 {
		return choices[defaultIdx].Key, nil
	}

	// Try numeric selection
	if n, err := strconv.Atoi(input); err == nil {
		if n >= 1 && n <= len(choices) {
			return choices[n-1].Key, nil
		}
		return "", fmt.Errorf("invalid selection: %d (choose 1-%d)", n, len(choices))
	}

	// Try matching by key name
	for _, c := range choices {
		if strings.EqualFold(input, c.Key) {
			return c.Key, nil
		}
	}

	return "", fmt.Errorf("invalid selection: %q", input)
}

type multiSelectKey int

const (
	multiSelectNone multiSelectKey = iota
	multiSelectUp
	multiSelectDown
	multiSelectToggle
	multiSelectConfirm
	multiSelectCancel
)

type multiSelectState struct {
	choices  []choice
	selected []bool
	cursor   int
}

func newMultiSelectState(choices []choice) multiSelectState {
	selected := make([]bool, len(choices))
	for i, item := range choices {
		selected[i] = item.IsDefault
	}
	return multiSelectState{choices: choices, selected: selected}
}

func (s multiSelectState) keys() []string {
	var out []string
	for i, item := range s.choices {
		if s.selected[i] {
			out = append(out, item.Key)
		}
	}
	return out
}

func (s *multiSelectState) apply(key multiSelectKey) (done, cancel bool) {
	if len(s.choices) == 0 {
		return true, true
	}
	switch key {
	case multiSelectUp:
		if s.cursor > 0 {
			s.cursor--
		}
	case multiSelectDown:
		if s.cursor < len(s.choices)-1 {
			s.cursor++
		}
	case multiSelectToggle:
		s.selected[s.cursor] = !s.selected[s.cursor]
	case multiSelectConfirm:
		return true, false
	case multiSelectCancel:
		return true, true
	}
	return false, false
}

func decodeMultiSelectKey(buf []byte) (multiSelectKey, int) {
	if len(buf) == 0 {
		return multiSelectNone, 0
	}
	switch buf[0] {
	case 3, 'q', 'Q':
		return multiSelectCancel, 1
	case '\r', '\n':
		return multiSelectConfirm, 1
	case ' ':
		return multiSelectToggle, 1
	case 'k', 'K':
		return multiSelectUp, 1
	case 'j', 'J':
		return multiSelectDown, 1
	case 0x1b:
		if len(buf) >= 3 && buf[1] == '[' {
			switch buf[2] {
			case 'A':
				return multiSelectUp, 3
			case 'B':
				return multiSelectDown, 3
			}
		}
		return multiSelectNone, 1
	}
	return multiSelectNone, 1
}

func renderMultiSelect(heading string, state multiSelectState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n", bold(heading))
	b.WriteString("  " + dim("↑/↓ move  space toggle  Enter confirm") + "\n\n")
	for i, item := range state.choices {
		marker := " "
		if i == state.cursor {
			marker = ">"
		}
		box := "[ ]"
		if state.selected[i] {
			box = "[x]"
		}
		line := fmt.Sprintf("  %s %s %s", marker, box, item.Display)
		if item.Description != "" {
			line += "  " + dim(item.Description)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func openInteractiveInput() (*os.File, func(), error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return os.Stdin, func() {}, nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	return tty, func() { _ = tty.Close() }, nil
}

func countRenderedLines(text string) int {
	n := strings.Count(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return n
	}
	return n + 1
}

func selectMany(ui *os.File, heading string, choices []choice) ([]string, error) {
	if len(choices) == 0 {
		return nil, fmt.Errorf("no options available")
	}
	state := newMultiSelectState(choices)
	in, closer, err := openInteractiveInput()
	if err != nil || !term.IsTerminal(int(in.Fd())) {
		if closer != nil {
			closer()
		}
		return state.keys(), nil
	}
	defer closer()
	fd := int(in.Fd())
	previous, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("enter raw terminal: %w", err)
	}
	defer func() { _ = term.Restore(fd, previous) }()

	out := ui
	if out == nil {
		out = os.Stderr
	}
	drawn := 0
	buf := make([]byte, 8)
	for {
		frame := renderMultiSelect(heading, state)
		if drawn > 0 {
			fmt.Fprintf(out, "\033[%dA", drawn)
		}
		fmt.Fprint(out, "\033[?25l")
		for _, line := range strings.Split(strings.TrimRight(frame, "\n"), "\n") {
			fmt.Fprintf(out, "\r\033[K%s\n", line)
		}
		drawn = countRenderedLines(frame)
		n, readErr := in.Read(buf)
		if readErr != nil {
			fmt.Fprint(out, "\033[?25h")
			return nil, fmt.Errorf("read selection: %w", readErr)
		}
		key, _ := decodeMultiSelectKey(buf[:n])
		done, cancel := state.apply(key)
		if !done {
			continue
		}
		fmt.Fprint(out, "\033[?25h")
		if cancel {
			return nil, fmt.Errorf("input canceled")
		}
		return state.keys(), nil
	}
}

// inputString asks for free-text input with an optional default.
func (p *prompter) inputString(label, defaultVal string) (string, error) {
	hint := ""
	if defaultVal != "" {
		hint = fmt.Sprintf(" [%s]", defaultVal)
	}
	fmt.Printf("  %s%s: ", label, hint)

	if !p.scanner.Scan() {
		return "", fmt.Errorf("input canceled")
	}

	input := strings.TrimSpace(p.scanner.Text())
	if input == "" {
		return defaultVal, nil
	}
	return input, nil
}

// inputPassword reads a password from the terminal with echo suppressed.
// Falls back to plain-text scanner reads when stdin is not a TTY (CI, pipes,
// tests). The trailing newline is consumed and trimmed.
func (p *prompter) inputPassword(label string) (string, error) {
	fmt.Printf("  %s: ", label)

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		buf, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimSpace(string(buf)), nil
	}

	// Non-TTY fallback: read a line via the scanner without echo suppression.
	if !p.scanner.Scan() {
		return "", fmt.Errorf("input canceled")
	}
	return strings.TrimSpace(p.scanner.Text()), nil
}

// dim applies a dim color to text (used for descriptions).
var dim = func(s string) string {
	return "\033[2m" + s + "\033[0m"
}
