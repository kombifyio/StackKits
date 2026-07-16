// Package lint implements `stackkit module lint` (ADR-0027 Decision 3, gates
// G1+G3): the deterministic module-hygiene checks that gate proposal PRs and
// tool-update PRs. It operates on an already-extracted cue.ModuleContract so
// the same rules run from the CLI, from CI, and from the scaffolder's
// self-check after rendering.
//
// Rules are intentionally derivable from the module CUE alone — no DB, no
// network — so the gate is reproducible on any checkout at any commit.
package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	skcue "github.com/kombifyio/stackkits/internal/cue"
)

// Severity classifies a finding. Errors fail the gate (in strict mode);
// warnings are advisory.
type Severity string

const (
	// SeverityError marks a hard violation that fails `--strict`.
	SeverityError Severity = "error"
	// SeverityWarn marks an advisory finding that never fails the gate.
	SeverityWarn Severity = "warn"
)

// Finding is a single lint result. Service is empty for module-scoped rules.
type Finding struct {
	Module   string   `json:"module"`
	Service  string   `json:"service,omitempty"`
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

func (f Finding) String() string {
	loc := f.Module
	if f.Service != "" {
		loc += "/" + f.Service
	}
	return fmt.Sprintf("%s  %s  [%s] %s", strings.ToUpper(string(f.Severity)), loc, f.Code, f.Message)
}

// floatingTagAliases are the well-known rolling tags that never pin a version.
var floatingTagAliases = map[string]bool{
	"latest": true, "stable": true, "edge": true, "release": true,
	"main": true, "master": true, "nightly": true, "dev": true,
	"rolling": true, "canary": true, "current": true, "beta": true,
}

var digitRe = regexp.MustCompile(`[0-9]`)

// isFloatingTag reports whether a tag fails the pin rule (ADR-0027 G1: image
// digest- or semver-pinned, no :latest). A tag is floating when it is empty,
// a known rolling alias, or carries no version component at all (e.g. "alpine",
// "apache"). Digests (sha256:...) and any tag with a numeric component
// (1.2.3, v3.6.13, 16-alpine, 30-apache) are treated as pinned.
func isFloatingTag(tag string) bool {
	t := strings.TrimSpace(tag)
	if t == "" {
		return true
	}
	if strings.HasPrefix(t, "sha256:") {
		return false
	}
	if floatingTagAliases[strings.ToLower(t)] {
		return true
	}
	// A pinned variant must carry a numeric version component somewhere
	// ("16-alpine", "v3", "2024.1"). A bare distro word ("alpine", "apache")
	// still floats within its line.
	return !digitRe.MatchString(t)
}

// secretishKeyRe matches env var names that carry credential material and
// therefore must never hold a plaintext literal in module CUE.
var secretishKeyRe = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|private[_-]?key|encryption[_-]?key|access[_-]?key|_key$|^key$)`)

// nonSecretKeyRe rescues keys that merely contain a secret-ish word but name a
// non-credential (a URL, host, name, scope list, id, path, flag). Without it,
// e.g. PROVIDERS_..._TOKEN_URL (an endpoint) or SECRETS=0 (a permission flag)
// would false-positive.
var nonSecretKeyRe = regexp.MustCompile(`(?i)(url|uri|endpoint|host|name|user|scopes?|path|dir|file|enabled|mode|port|public|issuer|_id$)`)

// numericOrBoolRe matches pure numeric / boolean literals, which are config
// flags rather than credentials.
var numericOrBoolRe = regexp.MustCompile(`(?i)^([0-9]+|true|false|yes|no|on|off)$`)

// isPlaintextSecret reports whether a value looks like a hardcoded secret
// rather than a template placeholder ("{{.x}}"), a secret reference
// ("secret://..."), a URL, or a numeric/boolean flag. Empty values are ignored.
func isPlaintextSecret(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	if strings.Contains(v, "{{") || strings.HasPrefix(v, "secret://") || strings.HasPrefix(v, "$") {
		return false
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return false
	}
	if numericOrBoolRe.MatchString(v) {
		return false
	}
	// Real secrets have length; short literals are almost always flags/labels.
	return len(v) >= 8
}

// Module runs every rule against a single extracted module contract and
// returns findings sorted deterministically (severity, then service, code).
func Module(mc skcue.ModuleContract) []Finding {
	slug := mc.Metadata.Name
	findings := moduleScopeFindings(slug, mc)

	names := make([]string, 0, len(mc.Services))
	for name := range mc.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		findings = append(findings, serviceFindings(slug, name, mc.Services[name])...)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity == SeverityError // errors first
		}
		if findings[i].Service != findings[j].Service {
			return findings[i].Service < findings[j].Service
		}
		return findings[i].Code < findings[j].Code
	})
	return findings
}

// moduleScopeFindings covers rules that concern the module as a whole.
func moduleScopeFindings(slug string, mc skcue.ModuleContract) []Finding {
	var out []Finding
	add := finder(slug, "", &out)

	switch mc.Metadata.Maturity {
	case "default", "opt-in", "draft":
		// ok
	case "":
		add("maturity-missing", SeverityError, "metadata.maturity is not set (must be default|opt-in|draft)")
	default:
		add("maturity-invalid", SeverityError, "metadata.maturity=%q is not one of default|opt-in|draft", mc.Metadata.Maturity)
	}

	// Draft modules must not claim canonical scenarios (Golden Rules §5.2).
	if mc.Metadata.Maturity == "draft" && len(mc.Metadata.TestScenarios) > 0 {
		add("draft-has-scenarios", SeverityError, "draft module claims testScenarios %v (drafts stay out of the canonical E2E dispatch)", mc.Metadata.TestScenarios)
	}

	// G3 placement lint: a docker-socket module cannot be managed-serverless.
	if mc.Requires != nil && mc.Requires.Infrastructure.DockerSocket &&
		mc.Placement != nil && mc.Placement.ManagedServerless {
		add("placement-docker-socket", SeverityError, "requires dockerSocket but placementSupport.managed_serverless=true (docker-socket modules cannot run managed-serverless)")
	}
	return out
}

// serviceFindings covers the per-service G1 rules.
func serviceFindings(slug, name string, svc skcue.ServiceDef) []Finding {
	var out []Finding
	add := finder(slug, name, &out)

	if isFloatingTag(svc.Tag) {
		add("image-not-pinned", SeverityError, "image %q uses floating tag %q — pin a semver/variant/digest", svc.Image, svc.Tag)
	}
	// A bounded one-shot reports success through its process exit status. A
	// synthetic long-running health check would keep an already-completed job
	// alive and misrepresent its lifecycle. Only the explicit automation +
	// restart=no + non-empty command contract receives this exception; every
	// daemon and incompletely-declared job still requires a health check.
	if svc.HealthCheck == nil && !isBoundedOneShot(svc) {
		add("healthcheck-missing", SeverityError, "no healthCheck declared (the healthCheck is the smoke-test assertion, ADR-0027 G4)")
	}
	if svc.Security == nil {
		add("security-missing", SeverityError, "no security block (require noNewPrivileges + capDrop [ALL])")
	} else {
		if !svc.Security.NoNewPrivileges {
			add("security-no-new-privileges", SeverityError, "security.noNewPrivileges must be true")
		}
		if !svc.Security.HasCapDropAll() {
			add("security-cap-drop-all", SeverityError, "security.capDrop must include \"ALL\"")
		}
	}
	if svc.TraefikEnabled || svc.TraefikRule != "" {
		if svc.AccessPolicy == nil || strings.TrimSpace(svc.AccessPolicy.OuterAuth) == "" {
			add("access-policy-missing", SeverityError, "routed service has no accessPolicy.outerAuth (no exposure without an explicit policy, Golden Rules §4.1/§4.8)")
		}
	}
	out = append(out, plaintextSecretFindings(slug, name, svc.Environment)...)
	return out
}

func isBoundedOneShot(svc skcue.ServiceDef) bool {
	return svc.Type == "automation" && svc.RestartPolicy == "no" && len(svc.Command) > 0
}

// plaintextSecretFindings flags hardcoded credentials in a service's env.
func plaintextSecretFindings(slug, name string, env map[string]string) []Finding {
	var out []Finding
	add := finder(slug, name, &out)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if secretishKeyRe.MatchString(k) && !nonSecretKeyRe.MatchString(k) && isPlaintextSecret(env[k]) {
			add("plaintext-secret", SeverityError, "environment %q holds a plaintext value — use a {{.template}} or secret:// reference", k)
		}
	}
	return out
}

// finder returns an appender bound to a module/service scope.
func finder(slug, service string, out *[]Finding) func(code string, sev Severity, format string, args ...any) {
	return func(code string, sev Severity, format string, args ...any) {
		*out = append(*out, Finding{
			Module:   slug,
			Service:  service,
			Code:     code,
			Severity: sev,
			Message:  fmt.Sprintf(format, args...),
		})
	}
}

// HasErrors reports whether any finding is error-severity.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// CountBySeverity returns (errors, warnings).
func CountBySeverity(findings []Finding) (errors, warnings int) {
	for _, f := range findings {
		switch f.Severity {
		case SeverityError:
			errors++
		case SeverityWarn:
			warnings++
		}
	}
	return errors, warnings
}
