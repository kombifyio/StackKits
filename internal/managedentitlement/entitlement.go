// Package managedentitlement is the fail-closed availability gate for
// publisher-tagged managed (S2/S3) StackKits surfaces.
//
// Standalone OSS paths stay ungated. A missing source, a check error, an empty
// decision, or any non-allow is denied before the caller may perform HTTP or
// other cost-bearing side effects. HMAC service tokens are not a source.
package managedentitlement

import (
	"context"
	"os"
	"strings"
	"sync"
)

const (
	// FeatureManagedServerless is the commercial feature required for kombify
	// to provision or operate the managed-serverless half of a StackKit.
	FeatureManagedServerless = "stackkits.managed_serverless"

	CapabilityTenantDeployment = "stackkits.managed.tenant_deployment"
	CapabilityBackupEnroll     = "stackkits.managed.backup.enroll"

	ErrorCode = "feature_entitlement_denied"

	ReasonSourceUnavailable = "entitlement_source_unavailable"
	ReasonCheckFailed       = "entitlement_check_failed"
	ReasonDenied            = "entitlement_denied"

	supportFeatureSource = "Stripe/FGA/Flagship entitlement chain"
)

// Source returns one availability decision for a managed capability.
type Source interface {
	Decide(ctx context.Context, capability string) (Decision, error)
}

// Decision is the single allow/deny result consumed by CLI, MCP, and jobs.
type Decision struct {
	Allowed          bool
	ReasonCode       string
	RequiredFeatures []string
	MissingFeatures  []string
}

type allowAllSource struct{}

func (allowAllSource) Decide(_ context.Context, _ string) (Decision, error) {
	return Decision{
		Allowed:          true,
		RequiredFeatures: []string{FeatureManagedServerless},
	}, nil
}

var (
	sourceMu sync.RWMutex
	source   Source
)

// CurrentSource is the process-wide entitlement source. Nil is deny.
func CurrentSource() Source {
	sourceMu.RLock()
	defer sourceMu.RUnlock()
	return source
}

// OverrideSource installs source for the current process and returns a restore
// function. Tests use this to inject an allow; production leaves the source nil
// until a Flagship/Stripe adapter is wired.
func OverrideSource(next Source) func() {
	sourceMu.Lock()
	prev := source
	source = next
	sourceMu.Unlock()
	return func() {
		sourceMu.Lock()
		source = prev
		sourceMu.Unlock()
	}
}

// AllowAll is an explicit positive decision used by tests that exercise the
// allowed managed path. It is not a production adapter.
func AllowAll() Source {
	return allowAllSource{}
}

// Evaluate fails closed for capability. Nil context is accepted.
func Evaluate(ctx context.Context, capability string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	capability = strings.TrimSpace(capability)
	if capability == "" {
		capability = CapabilityTenantDeployment
	}
	required := []string{FeatureManagedServerless}

	if localDevelopmentBypass() {
		return nil
	}

	current := CurrentSource()
	if current == nil {
		return deny(ReasonSourceUnavailable, capability, required, required)
	}

	decision, err := current.Decide(ctx, capability)
	if err != nil {
		return deny(ReasonCheckFailed, capability, required, required)
	}
	if decision.Allowed {
		return nil
	}

	missing := compactStrings(decision.MissingFeatures)
	if len(missing) == 0 {
		missing = required
	}
	reason := strings.TrimSpace(decision.ReasonCode)
	if reason == "" {
		reason = ReasonDenied
	}
	requiredFeatures := compactStrings(decision.RequiredFeatures)
	if len(requiredFeatures) == 0 {
		requiredFeatures = required
	}
	return deny(reason, capability, requiredFeatures, missing)
}

// localDevelopmentBypass is the one named development exception. Both switches
// are required so a single stale environment flag cannot authorize a
// cost-bearing managed path. It never satisfies SaaS or release evidence.
func localDevelopmentBypass() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("STACKKIT_ENV")), "development") &&
		truthyEnv("STACKKIT_ALLOW_LOCAL_MANAGED_ENTITLEMENT_E2E")
}

func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
