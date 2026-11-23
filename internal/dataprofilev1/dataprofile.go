// Package dataprofile is the data protection profile of a subject: residency,
// retention and locality (kombify workspace public StackKits standards/DATA-ARCHITECTURE.md
// section 5, D-2026-10-21-12). It is edition-neutral; the SaaS header transport
// lives in edgeauth (profile.go), the self-hosted Full Local profile comes from
// the boot environment.
//
// Every unknown, missing or malformed value is an error: a profile that cannot
// be resolved denies, it never falls back to Standard.
package dataprofile

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/denialv1"
	"github.com/kombifyio/stackkits/internal/editionv1"
)

// Version is the wire version of a profile without an ai scope member (v1).
// VersionAI is the wire version that carries the optional trailing "ai" member
// (D-2026-11-18-12). Signers emit v2 only when the ai scope differs from the
// cloud scope, so rollout order never matters; verifiers accept both. A v1
// profile means the ai scope equals the top-level (cloud) scope.
const (
	Version   = 1
	VersionAI = 2
)

// Residency is the res axis.
type Residency string

// Retention is the ret axis.
type Retention string

// Locality is the loc axis.
type Locality string

// Scope names where a profile value was set.
type Scope string

const (
	ResidencyGlobal Residency = "global"
	ResidencyEU     Residency = "eu"

	RetentionStandard Retention = "standard"
	RetentionZero     Retention = "zero"

	LocalityHosted Locality = "hosted"
	LocalityLocal  Locality = "local"

	ScopeAccount      Scope = "account"
	ScopeOrganization Scope = "organization"
	ScopeWorkspace    Scope = "workspace"
	ScopeSession      Scope = "session"
	ScopeEdition      Scope = "edition"
)

// Reason codes of the denial envelope.
const (
	ReasonUnresolvable        = "data_profile_unresolvable"
	ReasonCapabilityDenied    = "data_profile_capability_denied"
	ReasonProcessorIneligible = "data_profile_processor_ineligible"
	errorCode                 = "data_profile_denied"
)

// BootEnvVar selects the Full Local profile of the self-hosted edition.
const BootEnvVar = "KOMBIFY_DATA_PROFILE"

// FullLocal is the only accepted value of BootEnvVar.
const FullLocal = "full-local"

// AIScope is the res and ret pair of the account scope "ai": AI Platform,
// hosted SpeechKit, model providers, AI Gateway logs and prompt telemetry.
type AIScope struct {
	Res Residency `json:"res"`
	Ret Retention `json:"ret"`
}

// Profile is one resolved data protection profile. Rev is the revision of the
// stored record (0 for Standard without a record); TS is the unix second it
// was resolved.
//
// Res and Ret are the account scope "cloud" (data stored by Cloud, Paperwork,
// Homelab and the other product stores). AI, when set, is the account scope
// "ai" and the wire version is 2; when nil the ai scope equals Res and Ret
// (v1). Read the scope of what a request touches with CloudScope or AIScopeOf;
// when one request touches both, use Strictest. Loc is the edition's boot value
// and applies to both scopes.
//
// Resolver contract (D-2026-11-18-11): an absent account row is Standard (the
// Cloud resolver answers it); an absent or invalid signed header on a hosted
// route is never Standard and denies; an unreadable profile denies. Subjects
// with the "anonymous:" prefix and service subjects have no account and are
// never resolved to Standard: the resolver refuses them and the request denies.
type Profile struct {
	V     int       `json:"v"`
	Res   Residency `json:"res"`
	Ret   Retention `json:"ret"`
	Loc   Locality  `json:"loc"`
	Scope Scope     `json:"scope"`
	Rev   int64     `json:"rev"`
	TS    int64     `json:"ts"`
	AI    *AIScope  `json:"ai,omitempty"`
}

// Equal reports whether two profiles carry the same values. Use it instead of
// == because AI is a pointer.
func (p Profile) Equal(o Profile) bool {
	if (p.AI == nil) != (o.AI == nil) || (p.AI != nil && *p.AI != *o.AI) {
		return false
	}
	p.AI, o.AI = nil, nil
	return p == o
}

// WithAI returns p with the given ai scope: a v1 profile without an ai member
// when the scope equals the cloud scope, a v2 profile otherwise.
func (p Profile) WithAI(res Residency, ret Retention) Profile {
	if res == p.Res && ret == p.Ret {
		p.V, p.AI = Version, nil
		return p
	}
	p.V, p.AI = VersionAI, &AIScope{Res: res, Ret: ret}
	return p
}

// CloudScope is the profile for data stored by kombify products (Cloud,
// Paperwork, Homelab): the top-level res and ret, without an ai member.
func (p Profile) CloudScope() Profile {
	p.V, p.AI = Version, nil
	return p
}

// AIScopeOf is the profile for AI processing (AI Platform, hosted SpeechKit,
// model providers, AI Gateway logs, prompt telemetry): res and ret come from
// the ai member, or from the top level for a v1 profile.
func (p Profile) AIScopeOf() Profile {
	if p.AI != nil {
		p.Res, p.Ret = p.AI.Res, p.AI.Ret
	}
	p.V, p.AI = Version, nil
	return p
}

// Strictest is the profile for one request that touches both scopes: the
// strictest value per axis of the cloud and ai scopes.
func (p Profile) Strictest() Profile {
	out := p.CloudScope()
	if p.AI != nil {
		if p.AI.Res == ResidencyEU {
			out.Res = ResidencyEU
		}
		if p.AI.Ret == RetentionZero {
			out.Ret = RetentionZero
		}
	}
	return out
}

// Standard is the profile of a subject without a stored record.
func Standard() Profile {
	return Profile{V: Version, Res: ResidencyGlobal, Ret: RetentionStandard, Loc: LocalityHosted, Scope: ScopeAccount}
}

// Validate fails on any value outside the vocabulary. A v1 profile has no ai
// member; a v2 profile must have one that differs from the cloud scope (the
// shape every signer emits, so all codecs agree on one canonical form).
func (p Profile) Validate() error {
	switch {
	case p.V == Version && p.AI == nil:
	case p.V == VersionAI && p.AI != nil:
		switch p.AI.Res {
		case ResidencyGlobal, ResidencyEU:
		default:
			return fmt.Errorf("%s: ai residency %q", ReasonUnresolvable, p.AI.Res)
		}
		switch p.AI.Ret {
		case RetentionStandard, RetentionZero:
		default:
			return fmt.Errorf("%s: ai retention %q", ReasonUnresolvable, p.AI.Ret)
		}
		if p.AI.Res == p.Res && p.AI.Ret == p.Ret {
			return fmt.Errorf("%s: ai scope equals the cloud scope in a v2 profile", ReasonUnresolvable)
		}
	default:
		return fmt.Errorf("%s: version %d", ReasonUnresolvable, p.V)
	}
	switch p.Res {
	case ResidencyGlobal, ResidencyEU:
	default:
		return fmt.Errorf("%s: residency %q", ReasonUnresolvable, p.Res)
	}
	switch p.Ret {
	case RetentionStandard, RetentionZero:
	default:
		return fmt.Errorf("%s: retention %q", ReasonUnresolvable, p.Ret)
	}
	switch p.Loc {
	case LocalityHosted, LocalityLocal:
	default:
		return fmt.Errorf("%s: locality %q", ReasonUnresolvable, p.Loc)
	}
	switch p.Scope {
	case ScopeAccount, ScopeOrganization, ScopeWorkspace, ScopeSession, ScopeEdition:
	default:
		return fmt.Errorf("%s: scope %q", ReasonUnresolvable, p.Scope)
	}
	if p.Rev < 0 || p.TS < 0 {
		return fmt.Errorf("%s: negative revision or timestamp", ReasonUnresolvable)
	}
	return nil
}

// EU reports whether content and its processing must stay in the EU.
func (p Profile) EU() bool { return p.Res == ResidencyEU }

// Zero reports whether processing content must not be written durably.
func (p Profile) Zero() bool { return p.Ret == RetentionZero }

// Local reports whether nothing may leave the homelab.
func (p Profile) Local() bool { return p.Loc == LocalityLocal }

// Meet returns the strictest value per axis: eu over global, zero over
// standard, local over hosted. Scope, revision and timestamp come from the
// strictest contributor in that order of axes, else from the first profile.
// Meet of nothing is an error, never Standard.
func Meet(profiles ...Profile) (Profile, error) {
	if len(profiles) == 0 {
		return Profile{}, fmt.Errorf("%s: no profile to meet", ReasonUnresolvable)
	}
	out := profiles[0]
	if err := out.Validate(); err != nil {
		return Profile{}, err
	}
	aiOut := out.AIScopeOf()
	for _, p := range profiles[1:] {
		if err := p.Validate(); err != nil {
			return Profile{}, err
		}
		pa := p.AIScopeOf()
		tightened := false
		if pa.Res == ResidencyEU && aiOut.Res != ResidencyEU {
			aiOut.Res, tightened = ResidencyEU, true
		}
		if pa.Ret == RetentionZero && aiOut.Ret != RetentionZero {
			aiOut.Ret, tightened = RetentionZero, true
		}
		if p.Res == ResidencyEU && out.Res != ResidencyEU {
			out.Res, tightened = ResidencyEU, true
		}
		if p.Ret == RetentionZero && out.Ret != RetentionZero {
			out.Ret, tightened = RetentionZero, true
		}
		if p.Loc == LocalityLocal && out.Loc != LocalityLocal {
			out.Loc, tightened = LocalityLocal, true
		}
		if tightened {
			out.Scope = p.Scope
		}
		if p.Rev > out.Rev {
			out.Rev = p.Rev
		}
		if p.TS > out.TS {
			out.TS = p.TS
		}
	}
	// The ai scope is met on its own; the result is v2 only when it differs.
	return out.WithAI(aiOut.Res, aiOut.Ret), nil
}

// Canonical is the canonical JSON of p with a fixed key order; it is the
// signed form shared with the TypeScript twin.
func (p Profile) Canonical() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	out := fmt.Sprintf(`{"v":%d,"res":%q,"ret":%q,"loc":%q,"scope":%q,"rev":%d,"ts":%d`,
		p.V, p.Res, p.Ret, p.Loc, p.Scope, p.Rev, p.TS)
	if p.AI != nil {
		out += fmt.Sprintf(`,"ai":{"res":%q,"ret":%q}`, p.AI.Res, p.AI.Ret)
	}
	return out + "}", nil
}

// Encode returns the base64url (unpadded) canonical JSON carried in headers
// and job payloads.
func (p Profile) Encode() (string, error) {
	canonical, err := p.Canonical()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(canonical)), nil
}

// Decode parses an encoded profile strictly: unknown fields, non-canonical
// encodings and out-of-vocabulary values fail.
func Decode(encoded string) (Profile, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return Profile{}, fmt.Errorf("%s: encoding", ReasonUnresolvable)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var p Profile
	if decodeErr := dec.Decode(&p); decodeErr != nil {
		return Profile{}, fmt.Errorf("%s: payload", ReasonUnresolvable)
	}
	canonical, err := p.Canonical()
	if err != nil {
		return Profile{}, err
	}
	if canonical != string(raw) {
		return Profile{}, fmt.Errorf("%s: payload is not canonical", ReasonUnresolvable)
	}
	return p, nil
}

// FromBoot returns the edition profile of a self-hosted installation. Unset
// means hosted Standard; "full-local" is accepted only with selfhost-oss.
func FromBoot(e edition.Edition, value string) (Profile, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Standard(), nil
	}
	if value != FullLocal {
		return Profile{}, fmt.Errorf("%s: %s=%q", ReasonUnresolvable, BootEnvVar, value)
	}
	if e != edition.SelfhostOSS {
		return Profile{}, fmt.Errorf("%s: %s=%s needs edition %s, not %s", ReasonUnresolvable, BootEnvVar, FullLocal, edition.SelfhostOSS, e)
	}
	p := Standard()
	p.Loc, p.Scope = LocalityLocal, ScopeEdition
	return p, nil
}

// FromBootEnv reads BootEnvVar and the resolved edition.
func FromBootEnv() (Profile, error) {
	return FromBoot(edition.Resolve(), os.Getenv(BootEnvVar))
}

type ctxKey struct{}

// WithProfile stores a verified profile for downstream handlers.
func WithProfile(ctx context.Context, p Profile) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the verified profile. A missing profile is reported,
// never replaced by Standard.
func FromContext(ctx context.Context) (Profile, bool) {
	p, ok := ctx.Value(ctxKey{}).(Profile)
	return p, ok
}

// UnresolvableDenial is the envelope for a request whose profile is missing
// or invalid.
func UnresolvableDenial(requestID string) denial.Envelope {
	return denial.Envelope{
		ErrorCode:  errorCode,
		ReasonCode: ReasonUnresolvable,
		RequestID:  requestID,
		Retryable:  true,
		UserGuidance: denial.UserGuidance{
			Title:     "Your data protection settings could not be checked",
			Body:      "kombify did not run this request because it could not confirm your data protection settings.",
			NextSteps: []string{"Try again in a moment.", "If it keeps happening, contact support with the request ID."},
		},
	}
}

// CapabilityDenial is the envelope for a capability the profile does not
// allow, for example a non-EU model under EU residency.
func CapabilityDenial(requestID, capability, providerID string, p Profile) denial.Envelope {
	return denial.Envelope{
		ErrorCode:  errorCode,
		ReasonCode: ReasonCapabilityDenied,
		RequestID:  requestID,
		Capability: capability,
		ProviderID: providerID,
		Retryable:  false,
		UserGuidance: denial.UserGuidance{
			Title:     "Not available with your data protection settings",
			Body:      fmt.Sprintf("This feature cannot run under %s.", p.Describe()),
			NextSteps: []string{"Choose an option marked as available for your settings.", "Or change your data protection settings in your kombify account."},
		},
		SupportContext: map[string]any{"res": string(p.Res), "ret": string(p.Ret), "loc": string(p.Loc)},
	}
}

// Describe names the active options in plain words.
func (p Profile) Describe() string {
	var parts []string
	if p.EU() {
		parts = append(parts, "EU residency")
	}
	if p.Zero() {
		parts = append(parts, "Zero Retention")
	}
	if p.Local() {
		parts = append(parts, "Full Local")
	}
	if len(parts) == 0 {
		return "your current settings"
	}
	return strings.Join(parts, " and ")
}
