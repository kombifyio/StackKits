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
