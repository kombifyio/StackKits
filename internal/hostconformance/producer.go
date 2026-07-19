// Package hostconformance produces StackKits-owned, provider-neutral evidence
// about the host on which the probe is running. It never selects, provisions,
// addresses, or manages that host.
package hostconformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	ReceiptAPIVersion = "stackkit.host-conformance-receipt/v1"
	ReceiptKind       = "HostConformanceReceipt"
	DefaultValidity   = 30 * time.Minute
)

var (
	contentHashPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	contractIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	semanticVersion    = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

type Candidate struct {
	StackKitsVersion string
	Digest           string
}

// CandidateFromExecutable binds receipt evidence to the exact StackKits
// executable bytes that performed the local observation. Callers may inject a
// path for tests or packaged launchers; an empty path resolves the running
// executable.
func CandidateFromExecutable(stackKitsVersion, path string) (Candidate, error) {
	if !semanticVersion.MatchString(stackKitsVersion) {
		return Candidate{}, errors.New("stackkits candidate version is not canonical SemVer")
	}
	if path == "" {
		if runtime.GOOS == "linux" {
			// /proc/self/exe remains bound to the running inode even when an
			// upgrade atomically replaces the executable's original path.
			path = "/proc/self/exe"
		} else {
			resolved, err := os.Executable()
			if err != nil {
				return Candidate{}, fmt.Errorf("resolve running stackkits executable: %w", err)
			}
			path = resolved
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return Candidate{}, fmt.Errorf("open stackkits candidate executable: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Candidate{}, fmt.Errorf("inspect stackkits candidate executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Candidate{}, errors.New("stackkits candidate executable must be a regular file")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return Candidate{}, fmt.Errorf("hash stackkits candidate executable: %w", err)
	}
	return Candidate{
		StackKitsVersion: stackKitsVersion,
		Digest:           "sha256:" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

type OSFacts struct {
	Family       string
	Distribution string
	Version      string
}

type RuntimeFacts struct {
	Engine  string
	Version string
}

type VirtualizationFacts struct {
	Class  string
	Nested bool
}

type Facts struct {
	OS             OSFacts
	Architecture   string
	KernelRelease  string
	Runtime        RuntimeFacts
	Virtualization VirtualizationFacts
}

type Check struct {
	ID       string
	Category string
	Status   string
	Summary  string
}

type Observation struct {
	Facts  Facts
	Checks []Check
}

type Probe interface {
	Observe(context.Context) (Observation, error)
}

// OSPolicy is the only component allowed to turn an observed OS tuple into a
// compatibility statement. Host diagnostics cannot promote OS support.
type OSPolicy interface {
	EvaluateOS(OSFacts, Candidate) Check
}

type Producer struct {
	Probe    Probe
	Policy   OSPolicy
	Now      func() time.Time
	Validity time.Duration
}

type receiptBindingIdentity struct {
	bindingRef  string
	bindingHash string
	stackID     string
	nodeRef     string
	validUntil  time.Time
}

type UnverifiedOSPolicy struct{}

func (UnverifiedOSPolicy) EvaluateOS(OSFacts, Candidate) Check {
	return Check{
		ID:       "os-support-policy",
		Category: "os-compatibility",
		Status:   "unverified",
		Summary:  "No versioned StackKits OS support policy is available for this candidate",
	}
}

func (p Producer) Produce(ctx context.Context, binding resolvedplan.ExternalHostBinding, candidate Candidate) (resolvedplan.HostConformanceReceipt, error) {
	if p.Probe == nil {
		return nil, errors.New("host conformance probe is required")
	}
	if err := validateCandidate(candidate); err != nil {
		return nil, err
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	observedAt := now().UTC()
	if observedAt.IsZero() {
		return nil, errors.New("host conformance observation time is required")
	}
	identity, err := validateReceiptBinding(binding, candidate, observedAt)
	if err != nil {
		return nil, err
	}

	observation, err := p.Probe.Observe(ctx)
	if err != nil {
		return nil, fmt.Errorf("observe local host conformance: %w", err)
	}
	policy := p.Policy
	if policy == nil {
		policy = UnverifiedOSPolicy{}
	}
	osCheck := policy.EvaluateOS(observation.Facts.OS, candidate)
	checks := append([]Check{osCheck}, observation.Checks...)
	if err := validateObservation(observation.Facts, checks); err != nil {
		return nil, err
	}

	validity := p.Validity
	if validity <= 0 {
		validity = DefaultValidity
	}
	validUntil := observedAt.Add(validity)
	if validUntil.After(identity.validUntil) {
		validUntil = identity.validUntil
	}
	if !observedAt.Before(validUntil) {
		return nil, errors.New("external host binding has no remaining receipt validity window")
	}

	receipt := resolvedplan.HostConformanceReceipt{
		"apiVersion":       ReceiptAPIVersion,
		"kind":             ReceiptKind,
		"receiptRef":       receiptReference(identity.bindingRef, identity.bindingHash, candidate.Digest, observedAt),
		"bindingRef":       identity.bindingRef,
		"bindingHash":      identity.bindingHash,
		"stackId":          identity.stackID,
		"nodeRef":          identity.nodeRef,
		"stackkitsVersion": candidate.StackKitsVersion,
		"candidateDigest":  candidate.Digest,
		"observedAt":       formatTimestamp(observedAt),
		"validUntil":       formatTimestamp(validUntil),
		"facts":            factsDocument(observation.Facts),
		"checks":           checksDocument(checks),
		"result":           deriveResult(checks),
	}
	digest, err := resolvedplan.ComputeHostConformanceReceiptDigest(receipt)
	if err != nil {
		return nil, fmt.Errorf("compute host conformance receipt digest: %w", err)
	}
	receipt["receiptDigest"] = digest
	return receipt, nil
}

func validateCandidate(candidate Candidate) error {
	if !semanticVersion.MatchString(candidate.StackKitsVersion) {
		return errors.New("stackkits candidate version is not canonical SemVer")
	}
	if !contentHashPattern.MatchString(candidate.Digest) {
		return errors.New("stackkits candidate digest must be sha256:<64 lowercase hex>")
	}
	return nil
}

func validateReceiptBinding(binding resolvedplan.ExternalHostBinding, candidate Candidate, observedAt time.Time) (receiptBindingIdentity, error) {
	if err := resolvedplan.ValidateExternalHostBindingForReceipt(binding, observedAt); err != nil {
		return receiptBindingIdentity{}, fmt.Errorf("validate external host binding for receipt: %w", err)
	}
	values := map[string]string{}
	for _, field := range []string{"bindingRef", "bindingHash", "stackId", "nodeRef", "stackkitsVersion", "candidateDigest"} {
		value, err := bindingString(binding, field)
		if err != nil {
			return receiptBindingIdentity{}, err
		}
		values[field] = value
	}
	if candidate.StackKitsVersion != values["stackkitsVersion"] || candidate.Digest != values["candidateDigest"] {
		return receiptBindingIdentity{}, errors.New("running StackKits candidate does not match the external host binding")
	}
	if !contractIDPattern.MatchString(values["stackId"]) || !contractIDPattern.MatchString(values["nodeRef"]) {
		return receiptBindingIdentity{}, errors.New("external host binding stack and node identity must be canonical contract IDs")
	}
	validUntil, err := bindingTimestamp(binding, "validUntil")
	if err != nil {
		return receiptBindingIdentity{}, err
	}
	return receiptBindingIdentity{
		bindingRef: values["bindingRef"], bindingHash: values["bindingHash"],
		stackID: values["stackId"], nodeRef: values["nodeRef"], validUntil: validUntil,
	}, nil
}

// Attach returns a deep-cloned Inventory with the binding and receipt attached
// to exactly one existing node. canonicalInventoryHash excludes both envelopes,
// so this operation does not create a hash cycle.
func Attach(inventory resolvedplan.InventoryFacts, nodeRef string, binding resolvedplan.ExternalHostBinding, receipt resolvedplan.HostConformanceReceipt) (resolvedplan.InventoryFacts, error) {
	if !contractIDPattern.MatchString(nodeRef) {
		return nil, errors.New("inventory nodeRef must be a canonical contract ID")
	}
	if receipt["nodeRef"] != nodeRef || binding["nodeRef"] != nodeRef {
		return nil, errors.New("binding and receipt must target the requested inventory node")
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		return nil, fmt.Errorf("clone inventory: %w", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, fmt.Errorf("decode cloned inventory: %w", err)
	}
	nodes, ok := clone["nodes"].(map[string]any)
	if !ok {
		return nil, errors.New("inventory nodes object is required")
	}
	node, ok := nodes[nodeRef].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("inventory node %q does not exist", nodeRef)
	}
	node["externalHostBinding"] = map[string]any(binding)
	node["hostConformanceReceipt"] = map[string]any(receipt)
	return resolvedplan.InventoryFacts(clone), nil
}

func validateObservation(facts Facts, checks []Check) error {
	if err := validateFacts(facts); err != nil {
		return err
	}
	return validateChecks(checks)
}

func validateFacts(facts Facts) error {
	if facts.OS.Family != "linux" || !contractIDPattern.MatchString(facts.OS.Distribution) || strings.TrimSpace(facts.OS.Version) == "" || strings.ContainsAny(facts.OS.Version, " \t\r\n") {
		return errors.New("host conformance OS tuple is invalid")
	}
	if facts.Architecture != "amd64" && facts.Architecture != "arm64" {
		return fmt.Errorf("host architecture %q is unsupported by the receipt contract", facts.Architecture)
	}
	if strings.TrimSpace(facts.KernelRelease) == "" || strings.ContainsAny(facts.KernelRelease, " \t\r\n") {
		return errors.New("host kernel release is invalid")
	}
	if !allowedValue(facts.Runtime.Engine, "docker", "podman", "containerd", "none") || strings.TrimSpace(facts.Runtime.Version) == "" || strings.ContainsAny(facts.Runtime.Version, " \t\r\n") {
		return errors.New("host runtime facts are invalid")
	}
	if !allowedValue(facts.Virtualization.Class, "bare-metal", "kvm", "openvz", "lxc", "vmware", "hyperv", "xen", "oracle", "microsoft", "none") {
		return errors.New("host virtualization class is invalid")
	}
	return nil
}

func validateChecks(checks []Check) error {
	if len(checks) == 0 {
		return errors.New("at least one host conformance check is required")
	}
	seen := map[string]struct{}{}
	osChecks := 0
	for _, check := range checks {
		if !contractIDPattern.MatchString(check.ID) || !allowedValue(check.Category, "os-compatibility", "host-diagnostic") || !allowedValue(check.Status, "pass", "warning", "fail", "unverified") || strings.TrimSpace(check.Summary) == "" {
			return fmt.Errorf("host conformance check %q is invalid", check.ID)
		}
		if _, exists := seen[check.ID]; exists {
			return fmt.Errorf("host conformance check %q is duplicated", check.ID)
		}
		seen[check.ID] = struct{}{}
		if check.Category == "os-compatibility" {
			osChecks++
		}
	}
	if osChecks == 0 {
		return errors.New("at least one OS compatibility check is required")
	}
	return nil
}

func deriveResult(checks []Check) string {
	hasUnverified := false
	hasNonPass := false
	for _, check := range checks {
		if check.Status != "pass" {
			hasNonPass = true
		}
		if check.Category == "os-compatibility" && check.Status == "fail" {
			return "incompatible"
		}
		if check.Status == "unverified" {
			hasUnverified = true
		}
	}
	if hasUnverified {
		return "unverified"
	}
	if hasNonPass {
		return "degraded"
	}
	return "conformant"
}

func factsDocument(facts Facts) map[string]any {
	return map[string]any{
		"os": map[string]any{
			"family": facts.OS.Family, "distribution": facts.OS.Distribution, "version": facts.OS.Version,
		},
		"architecture": facts.Architecture,
		"kernel":       map[string]any{"release": facts.KernelRelease},
		"runtime":      map[string]any{"engine": facts.Runtime.Engine, "version": facts.Runtime.Version},
		"virtualization": map[string]any{
			"class": facts.Virtualization.Class, "nested": facts.Virtualization.Nested,
		},
	}
}

func checksDocument(checks []Check) []any {
	result := make([]any, 0, len(checks))
	for _, check := range checks {
		result = append(result, map[string]any{
			"id": check.ID, "category": check.Category, "status": check.Status, "summary": check.Summary,
		})
	}
	return result
}

func receiptReference(bindingRef, bindingHash, candidateDigest string, observedAt time.Time) string {
	hash := sha256.New()
	for _, value := range []string{bindingRef, bindingHash, candidateDigest, formatTimestamp(observedAt)} {
		_, _ = fmt.Fprintf(hash, "%d:%s\n", len(value), value)
	}
	return "host-conformance://sha256/" + hex.EncodeToString(hash.Sum(nil))
}

func bindingString(binding resolvedplan.ExternalHostBinding, field string) (string, error) {
	value, ok := binding[field].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("external host binding %s is required", field)
	}
	return value, nil
}

func bindingTimestamp(binding resolvedplan.ExternalHostBinding, field string) (time.Time, error) {
	value, err := bindingString(binding, field)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("external host binding %s must be RFC3339: %w", field, err)
	}
	return parsed.UTC(), nil
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func allowedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
