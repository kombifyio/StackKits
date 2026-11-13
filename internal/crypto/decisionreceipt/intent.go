package decisionreceipt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Principal is the acting principal a decision is bound to. The ledger signs
// it inside the intent, so a consumer can only recompute the intent digest
// when it knows the principal the caller's entry point authenticated.
type Principal struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// The principal kinds the ledger accepts.
const (
	PrincipalPerson        = "person"
	PrincipalAgentInstance = "agent_instance"
)

// ArgumentsDigestDomain separates the digest of one operation's canonical
// arguments from every other digest. The digest follows the platform's one
// digest convention (the ledger's intent and record digests): SHA-256 over
// the domain, a newline, the canonical JSON and a newline.
const ArgumentsDigestDomain = "kombify.stackkit-operation-arguments.digest.v1"

// The target kinds of the first slice (docs/DECISION-RECEIPTS.md, "Binding
// definition"). The installation itself has no kind: its reference is the
// node's own.
const (
	TargetApplication    = "application"
	TargetOwnApplication = "own-application"
	TargetRestore        = "restore"
	TargetChangeSet      = "change-set"
)

// Target is what one operation acts on, without the node: the node is added
// from custody, never from the caller. An empty Kind names the installation.
type Target struct {
	Kind string
	Ref  string
}

// Reference is the exact target reference the ledger signs as
// intent.resource: stackkit:<server id> for the installation, and
// stackkit:<server id>:<kind>:<ref> for everything on it.
func (t Target) Reference(node NodeIdentity) (string, error) {
	if !enrolledIDPattern.MatchString(node.ServerID) {
		return "", errors.New("the node identity is unavailable")
	}
	if t.Kind == "" {
		if t.Ref != "" {
			return "", errors.New("the installation target carries no reference")
		}
		return "stackkit:" + node.ServerID, nil
	}
	switch t.Kind {
	case TargetApplication, TargetOwnApplication, TargetRestore, TargetChangeSet:
	default:
		return "", fmt.Errorf("unknown target kind %q", t.Kind)
	}
	if t.Ref == "" || t.Ref != strings.TrimSpace(t.Ref) || len(t.Ref) > 200 || strings.ContainsFunc(t.Ref, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", errors.New("the target reference is invalid")
	}
	return "stackkit:" + node.ServerID + ":" + t.Kind + ":" + t.Ref, nil
}

// Request is one first-slice call as its command resolved it: the operation,
// the target on this node, the canonical arguments, the plan digest where the
// operation has one, the revision, and the acting principal.
type Request struct {
	Operation string
	Target    Target
	Arguments map[string]any
	// PlanSHA256 is the 64-hex digest of the reviewed plan, or empty when the
	// operation has none.
	PlanSHA256 string
	Revision   string
	Principal  Principal
}

// Facts are the resolved facts one receipt must bind: they are exactly the
// members of the intent the ledger signs.
type Facts struct {
	// Operation is the ledger's catalogue operation id.
	Operation string
	// Resource is the exact target reference.
	Resource string
	// ArgumentsSHA256 is the 64-hex digest of the canonical arguments.
	ArgumentsSHA256 string
	// PlanSHA256 is the 64-hex digest of the reviewed plan, empty when the
	// operation has none.
	PlanSHA256 string
	// Revision is the revision the operation was resolved against.
	Revision string
	// Principal is the acting principal the caller's entry point authenticated.
	Principal Principal
}

// Facts resolves one request on this node into the facts its receipt binds.
func (r Request) Facts(node NodeIdentity) (Facts, error) {
	if !IsFirstSlice(r.Operation) {
		return Facts{}, fmt.Errorf("operation %q is not in the first slice", r.Operation)
	}
	resource, err := r.Target.Reference(node)
	if err != nil {
		return Facts{}, err
	}
	arguments, err := ArgumentsDigest(r.Arguments)
	if err != nil {
		return Facts{}, err
	}
	if r.PlanSHA256 != "" && !ValidDigest(r.PlanSHA256) {
		return Facts{}, errors.New("the plan digest is not a lowercase SHA-256 digest")
	}
	revision := strings.TrimSpace(r.Revision)
	if revision == "" || revision != r.Revision || len(revision) > 128 {
		return Facts{}, errors.New("the revision is invalid")
	}
	if (r.Principal.Kind != PrincipalPerson && r.Principal.Kind != PrincipalAgentInstance) ||
		r.Principal.ID == "" || r.Principal.ID != strings.TrimSpace(r.Principal.ID) || len(r.Principal.ID) > 256 {
		return Facts{}, errors.New("the acting principal is invalid")
	}
	return Facts{
		Operation:       r.Operation,
		Resource:        resource,
		ArgumentsSHA256: arguments,
		PlanSHA256:      r.PlanSHA256,
		Revision:        revision,
		Principal:       r.Principal,
	}, nil
}

// ArgumentsDigest is the 64-hex SHA-256 of one operation's canonical
// arguments: SHA-256 over ArgumentsDigestDomain, a newline, the canonical
// JSON of the argument object (the ledger's canonicalDecisionJson form) and a
// newline. The producer of a decision and the consumer of its receipt build
// the same argument object; the ledger never sees the arguments themselves,
// only this digest.
func ArgumentsDigest(arguments map[string]any) (string, error) {
	if len(arguments) == 0 {
		return "", errors.New("the canonical arguments are empty")
	}
	canonical, err := canonicalJSON(arguments)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(ArgumentsDigestDomain + "\n" + canonical + "\n"))
	return hex.EncodeToString(sum[:]), nil
}

// IntentDigest recomputes the digest the ledger signed for one decision.
//
// The intent is the operation, its target, its risk, and the parameters that
// bind the digests, the revision and the acting principal; planSha256 is a
// member only when the operation has a plan. The digest is the
// domain-separated SHA-256 of the canonical JSON of that intent, exactly as
// the ledger computes it.
func IntentDigest(facts Facts) (string, error) {
	parameters := map[string]any{
		"argumentsSha256":     facts.ArgumentsSHA256,
		"revision":            facts.Revision,
		"actingPrincipalKind": facts.Principal.Kind,
		"actingPrincipalId":   facts.Principal.ID,
	}
	if facts.PlanSHA256 != "" {
		parameters["planSha256"] = facts.PlanSHA256
	}
	intent := map[string]any{
		"operation":  facts.Operation,
		"resource":   facts.Resource,
		"risk":       RiskDestructive,
		"parameters": parameters,
	}
	canonical, err := canonicalJSON(intent)
	if err != nil {
		return "", fmt.Errorf("intent canonical form: %w", err)
	}
	sum := sha256.Sum256([]byte(IntentDigestDomain + "\n" + canonical + "\n"))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
