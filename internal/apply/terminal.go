package apply

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/crypto"
	"golang.org/x/term"
)

// ttyPassphrasePrompter is the production PassphrasePrompter. It reads from
// /dev/stdin with echo suppressed (golang.org/x/term.ReadPassword) and
// verifies each attempt against the provided argon2id PHC hash before
// returning. On TTYs without a controlling terminal (CI, piped stdin) it
// falls back to a line-based read so non-interactive callers that pipe a
// passphrase still work.
type ttyPassphrasePrompter struct{}

// PromptAndVerify reads up to attempts passphrase entries, returning the
// first one that verifies against expectedHash. Mismatches print a hint to
// stderr and continue; a context-cancellation or read-error short-circuits
// the loop.
func (p *ttyPassphrasePrompter) PromptAndVerify(expectedHash string, attempts int) (string, error) {
	if attempts <= 0 {
		return "", fmt.Errorf("attempts must be positive")
	}
	for i := 0; i < attempts; i++ {
		plain, err := readPasswordFromTerminal(
			"Recovery passphrase (must match what you set in the wizard): ",
		)
		if err != nil {
			return "", err
		}
		if crypto.VerifyPassphrase(plain, expectedHash) {
			return plain, nil
		}
		_, _ = fmt.Fprintln(os.Stderr, "  Passphrase doesn't match. Try again.")
	}
	return "", fmt.Errorf("passphrase confirmation failed after %d attempts", attempts)
}

// readPasswordFromTerminal prompts on stderr (so prompts don't pollute
// piped stdout) and reads the response with echo suppressed when stdin is
// a TTY. Falls back to a buffered-line read for non-TTY stdin so CI runs
// with a piped passphrase work end-to-end.
//
// Note: we read from os.Stdin rather than /dev/tty so the implementation is
// portable to Windows builds. golang.org/x/term.ReadPassword on Stdin's fd
// works on both Unix (POSIX termios) and Windows (console-mode flip) in
// modern Go.
func readPasswordFromTerminal(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		buf, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(os.Stderr) // newline after the suppressed echo
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimSpace(string(buf)), nil
	}

	// Non-TTY fallback (CI, pipes, tests). Read a single line; no echo
	// suppression possible.
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return "", fmt.Errorf("read password: input canceled")
	}
	return strings.TrimSpace(scanner.Text()), nil
}
