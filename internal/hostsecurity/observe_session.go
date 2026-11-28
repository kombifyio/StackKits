package hostsecurity

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	observeUser          = "stackkit-observe"
	observePrefix        = "observe-"
	observeProfile       = "stackkit.observe/v1"
	observeCLI           = "/usr/local/bin/stackkit"
	observeShell         = "/usr/local/libexec/stackkit-observe"
	observePrincipalPath = SSHAccessDir + "/observe-principal"
	observeDropIn        = "/etc/ssh/sshd_config.d/00-stackkit-observe.conf"
	observeHome          = "/nonexistent"
	observeCommand       = "stackkit-observe"
	observeAuthorize     = "--observe-authorize"
)

var observePrincipalPattern = regexp.MustCompile(`^observe-[0-9a-f]{64}$`)

func ObserveEntrypoint(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case observeAuthorize:
		if len(args) != 3 || args[1] != ssh.CertAlgoED25519v01 || os.Geteuid() <= 0 {
			return 1, true
		}
		principal := admittedObserveCertificate(args[2])
		if principal == "" {
			return 1, true
		}
		_, err := fmt.Fprintln(os.Stdout, "restrict "+principal)
		if err != nil {
			return 1, true
		}
		return 0, true
	case observeShell:
		if os.Geteuid() <= 0 {
			return 1, true
		}
		if slices.Equal(args[1:], []string{"--probe"}) {
			return runObserveSandbox(observeProbe), true
		}
		command := os.Getenv("SSH_ORIGINAL_COMMAND")
		if !slices.Equal(args[1:], []string{"-c", observeCommand}) || strings.TrimSpace(command) == "" || len(command) > 4096 {
			return 1, true
		}
		authPath := os.Getenv("SSH_USER_AUTH")
		if !strings.HasPrefix(authPath, "/tmp/sshauth.") || strings.Contains(authPath[len("/tmp/sshauth."):], "/") {
			return 1, true
		}
		auth := strings.Fields(readObserveFile(authPath, 17000))
		if len(auth) != 3 || auth[0] != "publickey" || auth[1] != ssh.CertAlgoED25519v01 || admittedObserveCertificate(auth[2]) == "" {
			return 1, true
		}
		return runObserveSandbox(command), true
	default:
		return 0, false
	}
}

func readObserveFile(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return ""
	}
	return string(data)
}

func admittedObserveCertificate(encoded string) string {
	if len(encoded) > 16384 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	key, err := ssh.ParsePublicKey(blob)
	if err != nil {
		return ""
	}
	principal := strings.TrimSpace(readObserveFile(observePrincipalPath, 128))
	if !observeCertificateOK(key, principal, time.Now()) {
		return ""
	}
	cert := key.(*ssh.Certificate)
	for _, line := range managedLines([]byte(readObserveFile(SSHTrustedUserCAKeysPath, 32768))) {
		caKey, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || len(options) != 0 || !slices.Equal(caKey.Marshal(), cert.SignatureKey.Marshal()) {
			continue
		}
		checker := ssh.CertChecker{SupportedCriticalOptions: []string{"source-address"}}
		if checker.CheckCert(principal, cert) == nil {
			return principal
		}
	}
	return ""
}

func observeCertificateOK(key ssh.PublicKey, principal string, now time.Time) bool {
	cert, ok := key.(*ssh.Certificate)
	if !ok || cert.Type() != ssh.CertAlgoED25519v01 || cert.CertType != ssh.UserCert ||
		!observePrincipalPattern.MatchString(principal) || !slices.Equal(cert.ValidPrincipals, []string{principal}) ||
		len(cert.Extensions) != 0 || cert.ValidBefore <= cert.ValidAfter || cert.ValidBefore-cert.ValidAfter > 120 {
		return false
	}
	for option := range cert.CriticalOptions {
		if option != "source-address" {
			return false
		}
	}
	return cert.ValidAfter <= uint64(now.Unix()) && uint64(now.Unix()) < cert.ValidBefore
}

const observeProbe = `
set -eu
test -n "$(head -n 1 /host/cpuinfo)"
printf ready > /scratch/check
test "$(cat /scratch/check)" = ready
if (printf drift > /host/stackkit-observe-drift-probe) 2>/scratch/write-error; then exit 1; fi
printf 'write refused\n'
if cat /etc/shadow >/scratch/shadow-result 2>&1 || cat /etc/passwd >/scratch/passwd-result 2>&1; then exit 1; fi
test ! -e /run/docker.sock
printf 'protected read refused\n'
if output=$(busybox nc -w 1 127.0.0.1 1 2>&1); then exit 1; fi
case "$output" in *'Operation not permitted'*) ;; *) exit 1 ;; esac
printf 'onward connection refused\n'
`
