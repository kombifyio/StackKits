package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kombifyio/stackkits/internal/guardenrollment"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	connectServerEnv       = "KOMBI_SERVER"
	defaultConnectOrigin   = "https://techstack.kombify.io"
	connectTokenEnv        = "KOMBI_TOKEN" //nolint:gosec // environment variable name, not a credential
	connectInstallerLimit  = 4 << 20
	connectInstallerWindow = 30 * time.Second
)

// newConnectCommand binds a server that already runs the public package to the
// kombify account that created the pairing: the same one-time capability the
// Techstack setup prints for an own server, redeemed by the control plane's own
// installer. It needs no plan (D-2026-11-10-02); the control plane decides what
// the account may do after the server is bound.
func newConnectCommand() *cobra.Command {
	var server, tokenFile string
	command := &cobra.Command{
		Use:   "connect",
		Short: "Bind this server to your kombify account (a free account is enough)",
		Long: "Bind this server, which already runs the public package, to the kombify account that created the pairing " +
			"in the Techstack setup. The server shows its state in kombify Cloud afterwards; changes beyond that stay with " +
			"the paid plans. Paste the one-time pairing at the hidden terminal prompt. Automation can use --token-file or " +
			connectTokenEnv + ", never a token argument. The control plane origin comes from --server or " +
			connectServerEnv + " (public default " + defaultConnectOrigin + "). For a self-hosted control plane, copy the exact --server origin from its Techstack own-server setup in kombify Cloud. A server that is already bound reports its binding and changes nothing. Run it as root on Linux.",
		Example:       "  sudo stackkit connect --server https://techstack.kombify.io\n  sudo stackkit connect --server https://your-techstack.example --token-file /root/kombify-pairing",
		Annotations:   map[string]string{noDeployObservabilityAnnotation: "true"},
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConnect(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), server, tokenFile)
		},
	}
	command.Flags().StringVar(&server, "server", "", "control plane origin the pairing came from ("+connectServerEnv+", otherwise "+defaultConnectOrigin+"); copy self-hosted origins from the Techstack own-server setup")
	command.Flags().StringVar(&tokenFile, "token-file", "", "private file holding the one-time pairing (otherwise "+connectTokenEnv+", then a hidden terminal prompt)")
	return command
}

func runConnect(ctx context.Context, out, errOut io.Writer, server, tokenFile string) error {
	if bound, err := guardenrollment.Read(); err == nil {
		_, _ = fmt.Fprintf(out, "This server is already bound to kombify as %s. Nothing changed.\n", bound.Identity())
		return nil
	}
	if runtime.GOOS != "linux" {
		return errors.New("stackkit connect binds a Linux server; run it on the server that runs the public package")
	}
	origin, err := connectOrigin(firstNonEmptyConnect(server, os.Getenv(connectServerEnv), defaultConnectOrigin))
	if err != nil {
		return err
	}
	token, err := connectToken(tokenFile, errOut)
	if err != nil {
		return err
	}
	script, err := fetchConnectInstaller(ctx, origin)
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("stackkit connect installs the kombify Guard service and needs root; run it with sudo")
	}
	// The capability reaches the installer through its environment only, as the
	// setup's own command does, so it never appears in an argument list.
	install := exec.CommandContext(ctx, "bash", "-s") //nolint:gosec // fixed interpreter; the script is the control plane's installer
	install.Stdin = bytes.NewReader(script)
	install.Stdout, install.Stderr = out, errOut
	install.Env = append(connectInstallerEnvironment(os.Environ()), connectServerEnv+"="+origin, connectTokenEnv+"="+token, "TECHSTACK_AS_SERVICE=1")
	if err := install.Run(); err != nil {
		return fmt.Errorf("the kombify installer did not finish: %w", err)
	}
	if bound, err := guardenrollment.Read(); err == nil {
		_, _ = fmt.Fprintf(out, "This server is bound to kombify as %s. Its state shows in kombify Cloud.\n", bound.Identity())
		return nil
	}
	return errors.New("the installer finished but this server shows no binding; run stackkit connect again with a new pairing")
}

// connectInstallerEnvironment drops every KOMBI_* and TECHSTACK_* variable of the caller: the installer acts on
// them (registration-only, existing-runtime identity, binary selection), so only the origin, the capability and
// the service switch reach it.
func connectInstallerEnvironment(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if strings.HasPrefix(entry, "KOMBI_") || strings.HasPrefix(entry, "TECHSTACK_") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func firstNonEmptyConnect(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// connectOrigin accepts an https origin, or loopback http for a local control
// plane, and nothing else: the installer script runs as root.
func connectOrigin(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("use https://techstack.kombify.io for the public product; for self-hosted Techstack copy the origin from its own-server setup with --server or " + connectServerEnv)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("the control plane origin must be a plain HTTPS origin; use https://techstack.kombify.io or copy --server from your self-hosted Techstack own-server setup")
	}
	ip := net.ParseIP(parsed.Hostname())
	loopback := strings.EqualFold(parsed.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopback) {
		return "", errors.New("the control plane origin must use https (loopback http is allowed for a local control plane)")
	}
	// A private LAN bridge presents a self-signed key that only the pin of its own install command makes
	// trustworthy; this command has no pin, so that setup keeps using its printed command.
	if parsed.Scheme == "https" && ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return "", errors.New("a private LAN control plane needs the pinned install command the Techstack setup prints; run that command instead")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func connectToken(tokenFile string, errOut io.Writer) (string, error) {
	var raw string
	if tokenFile != "" {
		content, err := os.ReadFile(tokenFile) //nolint:gosec // operator-named capability file
		if err != nil {
			return "", fmt.Errorf("read the pairing capability: %w", err)
		}
		raw = string(content)
	} else {
		raw = os.Getenv(connectTokenEnv)
		if strings.TrimSpace(raw) == "" {
			prompted, err := promptConnectToken(errOut)
			if err != nil {
				return "", err
			}
			raw = prompted
		}
	}
	token := strings.TrimSpace(raw)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("the pairing is empty or invalid; copy a new pairing from the Techstack own-server setup and paste it at the hidden prompt")
	}
	return token, nil
}

func promptConnectToken(errOut io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("paste the pairing at a terminal: sudo stackkit connect; for automation use --token-file (mode 0600)")
	}
	state, err := term.GetState(fd)
	if err != nil {
		return "", errors.New("cannot read the terminal state; use --token-file")
	}
	defer func() { _ = term.Restore(fd, state); _, _ = fmt.Fprintln(errOut) }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	type answer struct {
		secret []byte
		err    error
	}
	answers := make(chan answer)
	_, _ = fmt.Fprint(errOut, "Pairing (hidden): ")
	go func() {
		secret, err := term.ReadPassword(fd)
		select {
		case answers <- answer{secret, err}:
		case <-ctx.Done():
			clear(secret)
		}
	}()
	select {
	case <-ctx.Done():
		return "", errors.New("pairing entry cancelled")
	case result := <-answers:
		defer clear(result.secret)
		if result.err != nil {
			return "", errors.New("could not read the pairing from the terminal; try again or use --token-file")
		}
		return string(result.secret), nil
	}
}

func fetchConnectInstaller(ctx context.Context, origin string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, connectInstallerWindow)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/install.sh", nil)
	if err != nil {
		return nil, err
	}
	// A redirect could leave the validated origin, so none is followed.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request) //nolint:gosec // origin validated by connectOrigin
	if err != nil {
		return nil, fmt.Errorf("cannot reach the Techstack installer at %s: %w; check this server’s network access and copy the --server origin from the Techstack own-server setup (public default %s)", origin, err, defaultConnectOrigin)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the Techstack installer at %s/install.sh answered HTTP %d; copy the correct --server origin from the Techstack own-server setup (public default %s)", origin, response.StatusCode, defaultConnectOrigin)
	}
	script, err := io.ReadAll(io.LimitReader(response.Body, connectInstallerLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read the control plane installer: %w", err)
	}
	if len(script) == 0 || len(script) > connectInstallerLimit {
		return nil, errors.New("the control plane installer has an unexpected size")
	}
	if !bytes.HasPrefix(script, []byte("#!/bin/sh\n")) && !bytes.HasPrefix(script, []byte("#!/bin/bash\n")) {
		return nil, fmt.Errorf("%s/install.sh did not return a shell installer; copy the --server origin from the Techstack own-server setup (public default %s)", origin, defaultConnectOrigin)
	}
	return script, nil
}
