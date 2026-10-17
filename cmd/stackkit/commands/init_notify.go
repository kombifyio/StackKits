package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// askOwnerNtfyTopicIfTerminal asks the one guided-mode question about how the
// homelab reaches its owner. It prompts only on a real terminal; Enter skips,
// which leaves spec.notifications absent and status reporting "not configured".
// Expert mode declares channels in the spec or with --notify-ntfy-topic.
func askOwnerNtfyTopicIfTerminal(ui *os.File) (string, error) {
	in, closer, err := openInteractiveInput()
	if closer != nil {
		defer closer()
	}
	if err != nil || !term.IsTerminal(int(in.Fd())) {
		return "", nil
	}
	_, _ = fmt.Fprint(ui, "\n  Where should your homelab reach you? ntfy topic (Enter to skip): ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", nil
	}
	return strings.TrimSpace(line), nil
}
