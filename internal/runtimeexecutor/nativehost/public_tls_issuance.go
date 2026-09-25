package nativehost

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
)

// A route without a certificate only shows up at the public edge as a TLS
// failure (Cloudflare answers HTTP 526). The reason lives in the node-local
// Traefik ACME resolver: the certificate authority's RFC 8555 problem, for
// example a rate limit that no retry can pass before its retry-after time
// (managed Cloud Kit attempts 2026-09-07). The reader below turns the
// resolver's own log into that closed fact. Log text, account data and
// certificate material never leave it.

// publicTLSIssuanceRefusal is the closed, secret-free fact read from the ACME
// resolver for one declared host.
type publicTLSIssuanceRefusal struct {
	// ProblemType is the RFC 8555 problem type suffix, for example
	// "rateLimited". Only registered problem types are reported.
	ProblemType string
	// RetryAfter is the time the certificate authority named for the next
	// attempt, or zero when it named none.
	RetryAfter time.Time
}

type publicTLSIssuanceEvidence interface {
	IssuanceRefusal(ctx context.Context, host string) (publicTLSIssuanceRefusal, bool)
}

// publicTLSIssuanceRefusedError is a leaf error on purpose: bounded Apply
// causes keep only leaves, and this leaf must carry the route, the probe
// outcome and the certificate authority's closed refusal together.
type publicTLSIssuanceRefusedError struct {
	routeRef string
	address  string
	probe    string
	refusal  publicTLSIssuanceRefusal
}

func (e *publicTLSIssuanceRefusedError) Error() string {
	message := fmt.Sprintf("declared HTTPS route %q at https://%s has no issued certificate: the ACME certificate authority refused issuance (urn:ietf:params:acme:error:%s)",
		e.routeRef, e.address, e.refusal.ProblemType)
	if !e.refusal.RetryAfter.IsZero() {
		message += ", retry after " + e.refusal.RetryAfter.UTC().Format(time.RFC3339)
	}
	if e.probe != "" {
		message += "; last probe: " + e.probe
	}
	return message
}

// issuanceTerminal stops the issuance wait: a rate-limited authority cannot
// issue inside the wait budget, so waiting longer only delays the outcome.
func (e *publicTLSIssuanceRefusedError) issuanceTerminal() bool {
	return e.refusal.ProblemType == "rateLimited"
}

// acmeProblemTypes is the closed RFC 8555/8657 problem vocabulary a refusal may
// report. Anything else is not a certificate authority problem we can name.
var acmeProblemTypes = map[string]struct{}{
	"accountDoesNotExist": {}, "alreadyRevoked": {}, "badCSR": {}, "badNonce": {}, "badPublicKey": {},
	"badRevocationReason": {}, "badSignatureAlgorithm": {}, "caa": {}, "compound": {}, "connection": {},
	"dns": {}, "externalAccountRequired": {}, "incorrectResponse": {}, "invalidContact": {}, "malformed": {},
	"orderNotReady": {}, "rateLimited": {}, "rejectedIdentifier": {}, "serverInternal": {}, "tls": {},
	"unauthorized": {}, "unsupportedContact": {}, "unsupportedIdentifier": {}, "userActionRequired": {},
}

var (
	acmeProblemPattern = regexp.MustCompile(`urn:ietf:params:acme:error:([A-Za-z]+)`)
	ansiEscapePattern  = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
)

// parseACMEIssuanceRefusal returns the latest refusal the resolver logged for
// exactly host. A line names the host only when the host is not part of a
// longer name, so "a.example" never matches a refusal for "xa.example".
func parseACMEIssuanceRefusal(logs []byte, host string) (publicTLSIssuanceRefusal, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return publicTLSIssuanceRefusal{}, false
	}
	var latest publicTLSIssuanceRefusal
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(logs))
	scanner.Buffer(make([]byte, 0, 64<<10), 256<<10)
	for scanner.Scan() {
		line := ansiEscapePattern.ReplaceAllString(scanner.Text(), "")
		problem := acmeProblemPattern.FindStringSubmatch(line)
		if len(problem) != 2 || !mentionsExactHost(strings.ToLower(line), host) {
			continue
		}
		if _, known := acmeProblemTypes[problem[1]]; !known {
			continue
		}
		refusal := publicTLSIssuanceRefusal{ProblemType: problem[1]}
		refusal.RetryAfter, _ = applyoutcome.AuthorityRetryAfter(line)
		latest, found = refusal, true
	}
	return latest, found
}

func mentionsExactHost(line, host string) bool {
	for offset := 0; offset < len(line); {
		index := strings.Index(line[offset:], host)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(host)
		if (start == 0 || !hostNameByte(line[start-1])) && (end == len(line) || !hostNameByte(line[end])) {
			return true
		}
		offset = start + 1
	}
	return false
}

func hostNameByte(value byte) bool {
	return value == '-' || value == '.' || value == '_' || (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9')
}

// publicTLSRouterContainers are the exact Compose containers of the Cloud core
// router that owns the ACME resolver. Only one of them exists on a node.
var publicTLSRouterContainers = []string{"stackkit-cloud-core-standalone-router-1", "stackkit-cloud-core-router-1"}

const publicTLSResolverLogTail = "500"

type dockerTraefikIssuanceEvidence struct{}

func (dockerTraefikIssuanceEvidence) IssuanceRefusal(ctx context.Context, host string) (publicTLSIssuanceRefusal, bool) {
	for _, container := range publicTLSRouterContainers {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		command := exec.CommandContext(readCtx, "docker", "logs", "--tail", publicTLSResolverLogTail, container) //nolint:gosec // fixed executable and closed container names
		command.Env = []string{"LANG=C", "LC_ALL=C"}
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			continue
		}
		return parseACMEIssuanceRefusal(output, host)
	}
	return publicTLSIssuanceRefusal{}, false
}

var _ publicTLSIssuanceEvidence = dockerTraefikIssuanceEvidence{}
