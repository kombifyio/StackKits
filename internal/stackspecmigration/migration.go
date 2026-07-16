// Package stackspecmigration contains the bounded compatibility seam for the
// one-minor StackSpec v1 -> v2 migration described by ADR-0029.
//
// The package deliberately stops at architecture normalization. It does not
// compile a ResolvedPlan and it does not claim that a normalized architecture
// is a complete, CUE-valid StackSpec v2. The future compiler must combine this
// result with explicit topology, capability, provider, access, and inventory
// inputs before any generator or runtime can consume it.
package stackspecmigration

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	APIVersionV1                  = "stackkit/v1"
	APIVersionV2Alpha1            = "stackkit/v2alpha1"
	APIVersionMigrationProjection = "stackkit.migration/v1"
	KindStackSpec                 = "StackSpec"
	KindMigrationProjection       = "StackSpecMigrationProjection"
)

// KitProfile is the canonical v2 product identity. Context never selects it.
type KitProfile string

const (
	KitProfileBasement KitProfile = "basement-kit"
	KitProfileCloud    KitProfile = "cloud-kit"
	KitProfileModern   KitProfile = "modern-homelab"

	// LegacyHAKitSlug is recognized only by the bounded v1 migration seam. It
	// is never a canonical KitProfile or an active product/discovery value.
	LegacyHAKitSlug = "ha-kit"
)

// SiteKind is the canonical v2 locality dimension.
type SiteKind string

const (
	SiteKindHome  SiteKind = "home"
	SiteKindCloud SiteKind = "cloud"
)

// MigrationStatus communicates whether the normalization is usable by the
// next migration stage or requires additional operator intent.
type MigrationStatus string

const (
	MigrationStatusReady     MigrationStatus = "ready-for-shadow-resolution"
	MigrationStatusCompleted MigrationStatus = "completed-v2"
	MigrationStatusBlocked   MigrationStatus = "blocked"
)

// Options contains explicit operator decisions. TargetKitProfile is never
// inferred from context. It is only needed when the legacy product identity is
// missing or when the operator explicitly accepts a supported reclassification.
type Options struct {
	TargetKitProfile KitProfile `json:"targetKitProfile,omitempty" yaml:"targetKitProfile,omitempty"`
}

// NormalizedArchitecture is the architecture-only output of v1 normalization.
// It is intentionally not named StackSpecV2: topology and security contracts
// still have to be supplied and CUE-validated by the compiler.
type NormalizedArchitecture struct {
	APIVersion       string       `json:"apiVersion" yaml:"apiVersion"`
	Kind             string       `json:"kind" yaml:"kind"`
	KitProfile       KitProfile   `json:"kitProfile" yaml:"kitProfile"`
	PrimarySite      SiteTarget   `json:"primarySite" yaml:"primarySite"`
	AuthoritySiteRef string       `json:"authoritySiteRef" yaml:"authoritySiteRef"`
	NodeDefaults     NodeDefaults `json:"nodeDefaults" yaml:"nodeDefaults"`
}

// SiteTarget is the single-site target that can be derived from a non-Modern
// v1 spec without inventing a hybrid topology.
type SiteTarget struct {
	ID   string   `json:"id" yaml:"id"`
	Kind SiteKind `json:"kind" yaml:"kind"`
}

// NodeDefaults records how the deprecated stack-level context is projected to
// legacy nodes. HardwareProfile is populated only for context=pi; local/cloud
// never invent a hardware class.
type NodeDefaults struct {
	SiteRef               string `json:"siteRef" yaml:"siteRef"`
	HardwareProfile       string `json:"hardwareProfile,omitempty" yaml:"hardwareProfile,omitempty"`
	ApplyToAllLegacyNodes bool   `json:"applyToAllLegacyNodes" yaml:"applyToAllLegacyNodes"`
}

// Report is emitted for successful and blocked migrations. Callers must show
// it to the operator before explicit acceptance; it is not optional telemetry.
type Report struct {
	SourceVersion              string          `json:"sourceVersion" yaml:"sourceVersion"`
	TargetVersion              string          `json:"targetVersion" yaml:"targetVersion"`
	Status                     MigrationStatus `json:"status" yaml:"status"`
	RequiresExplicitAcceptance bool            `json:"requiresExplicitAcceptance" yaml:"requiresExplicitAcceptance"`
	Decisions                  []Decision      `json:"decisions" yaml:"decisions"`
	Warnings                   []Warning       `json:"warnings" yaml:"warnings"`
	ManualActions              []ManualAction  `json:"manualActions" yaml:"manualActions"`
	Blockers                   []Blocker       `json:"blockers" yaml:"blockers"`
}

// Decision makes every deterministic normalization or explicit operator
// choice auditable.
type Decision struct {
	Code   string `json:"code" yaml:"code"`
	Field  string `json:"field" yaml:"field"`
	From   string `json:"from,omitempty" yaml:"from,omitempty"`
	To     string `json:"to" yaml:"to"`
	Reason string `json:"reason" yaml:"reason"`
}

// Warning describes a non-blocking compatibility concern.
type Warning struct {
	Code           string `json:"code" yaml:"code"`
	Field          string `json:"field" yaml:"field"`
	Message        string `json:"message" yaml:"message"`
	RequiredAction string `json:"requiredAction,omitempty" yaml:"requiredAction,omitempty"`
}

// ManualAction records information that cannot be transferred into a complete
// StackSpecV2 without an operator or a later shadow-resolution stage. It must
// never be silently dropped by a write-mode migration command.
type ManualAction struct {
	Code     string   `json:"code" yaml:"code"`
	Fields   []string `json:"fields" yaml:"fields"`
	Message  string   `json:"message" yaml:"message"`
	Required bool     `json:"required" yaml:"required"`
}

// Blocker describes missing or contradictory intent that ADR-0029 forbids the
// migration from guessing.
type Blocker struct {
	Code                 string       `json:"code" yaml:"code"`
	Field                string       `json:"field" yaml:"field"`
	Message              string       `json:"message" yaml:"message"`
	RequiredInputs       []string     `json:"requiredInputs" yaml:"requiredInputs"`
	SuggestedKitProfiles []KitProfile `json:"suggestedKitProfiles,omitempty" yaml:"suggestedKitProfiles,omitempty"`
}

// MigrationError is returned with the same blockers contained in Report.
// errors.As can be used to present machine-actionable guidance.
type MigrationError struct {
	Blockers []Blocker
}

func (e *MigrationError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return "StackSpec migration is blocked"
	}

	messages := make([]string, 0, len(e.Blockers))
	for _, blocker := range e.Blockers {
		messages = append(messages, blocker.Message)
	}
	return "StackSpec migration is blocked: " + strings.Join(messages, "; ")
}

// MigrateV1 normalizes only architecture dimensions that are deterministic.
// It never mutates spec. Context validates/maps locality and Pi hardware; it
// never changes the selected KitProfile.
func MigrateV1(spec *models.StackSpec, options Options) (NormalizedArchitecture, Report, error) {
	report := Report{
		SourceVersion:              APIVersionV1,
		TargetVersion:              APIVersionV2Alpha1,
		Status:                     MigrationStatusBlocked,
		RequiresExplicitAcceptance: true,
		Decisions:                  []Decision{},
		Warnings:                   []Warning{},
		ManualActions:              []ManualAction{},
		Blockers:                   []Blocker{},
	}

	if spec == nil {
		return blocked(report, Blocker{
			Code:           "spec.nil",
			Field:          "spec",
			Message:        "a nil StackSpec cannot be migrated",
			RequiredInputs: []string{"stackSpec"},
		})
	}

	target, targetSet, targetBlocker := normalizeTarget(options.TargetKitProfile)
	if targetBlocker != nil {
		return blocked(report, *targetBlocker)
	}

	legacyKit := strings.ToLower(strings.TrimSpace(spec.StackKit))
	contextSite, hardwareProfile, contextSet, contextBlocker := normalizeContext(spec.Context, &report)
	if contextBlocker != nil {
		return blocked(report, *contextBlocker)
	}

	selected, sourceProfile, selectionBlockers := selectKitProfile(legacyKit, target, targetSet, contextSite, contextSet, &report)
	if len(selectionBlockers) > 0 {
		return blocked(report, selectionBlockers...)
	}

	if selected == KitProfileModern {
		return blocked(report, modernTopologyBlocker())
	}

	expectedSite := siteKindForProfile(selected)
	if contextSet && contextSite != expectedSite {
		blocker := contextConflictBlocker(sourceProfile, selected, contextSite, targetSet)
		return blocked(report, blocker)
	}

	if !contextSet {
		contextSite = expectedSite
		report.Decisions = append(report.Decisions, Decision{
			Code:   "site.from-kit-profile",
			Field:  "sites[0].kind",
			To:     string(expectedSite),
			Reason: "legacy context is absent; the explicitly selected KitProfile supplies its required single-site locality",
		})
		report.Warnings = append(report.Warnings, Warning{
			Code:           "context.missing",
			Field:          "context",
			Message:        "legacy context is absent; no runtime detection was used to select or change the kit",
			RequiredAction: "review the generated Site and accept it explicitly before shadow resolution",
		})
	}

	siteID := string(contextSite)
	result := NormalizedArchitecture{
		APIVersion:       APIVersionMigrationProjection,
		Kind:             KindMigrationProjection,
		KitProfile:       selected,
		PrimarySite:      SiteTarget{ID: siteID, Kind: contextSite},
		AuthoritySiteRef: siteID,
		NodeDefaults: NodeDefaults{
			SiteRef:               siteID,
			HardwareProfile:       hardwareProfile,
			ApplyToAllLegacyNodes: true,
		},
	}
	report.ManualActions = append(report.ManualActions, ManualAction{
		Code: "projection.complete-v2-spec",
		Fields: []string{
			"install", "system", "storage", "network", "nodes", "capabilities",
			"addons", "access", "routes", "modules", "data",
		},
		Message:  "the architecture projection must be combined with explicit deployment intent and CUE shadow resolution before it becomes a StackSpecV2",
		Required: true,
	})

	report.Status = MigrationStatusReady
	return result, report, nil
}

// MigrateDocument is the safe entry for file/API migration. In contrast to
// MigrateV1 it has access to the lossless reader metadata and therefore blocks
// unknown legacy fields before a caller could write a lossy projection.
func MigrateDocument(document Document, options Options) (NormalizedArchitecture, Report, error) {
	if document.Version != SourceVersionV1 || document.Legacy == nil {
		report := Report{
			SourceVersion:              string(document.Version),
			TargetVersion:              APIVersionV2Alpha1,
			Status:                     MigrationStatusBlocked,
			RequiresExplicitAcceptance: true,
			Decisions:                  []Decision{},
			Warnings:                   []Warning{},
			ManualActions:              []ManualAction{},
			Blockers:                   []Blocker{},
		}
		return blocked(report, Blocker{
			Code:           "document.not-v1",
			Field:          "apiVersion",
			Message:        "only a losslessly read v1 StackSpec can enter the v1 migration adapter",
			RequiredInputs: []string{"v1 StackSpec"},
		})
	}

	if len(document.UnknownV1Fields) > 0 {
		report := Report{
			SourceVersion:              APIVersionV1,
			TargetVersion:              APIVersionV2Alpha1,
			Status:                     MigrationStatusBlocked,
			RequiresExplicitAcceptance: true,
			Decisions:                  []Decision{},
			Warnings:                   []Warning{},
			ManualActions: []ManualAction{{
				Code:     "v1.unknown-fields-review",
				Fields:   append([]string(nil), document.UnknownV1Fields...),
				Message:  "unknown v1 fields have no governed v2 mapping and must be reviewed before migration",
				Required: true,
			}},
			Blockers: []Blocker{},
		}
		return blocked(report, Blocker{
			Code:           "v1.unknown-fields",
			Field:          "document",
			Message:        fmt.Sprintf("v1 StackSpec contains unknown fields: %s", strings.Join(document.UnknownV1Fields, ", ")),
			RequiredInputs: append([]string(nil), document.UnknownV1Fields...),
		})
	}

	return MigrateV1(document.Legacy, options)
}

func normalizeTarget(raw KitProfile) (KitProfile, bool, *Blocker) {
	value := KitProfile(strings.ToLower(strings.TrimSpace(string(raw))))
	if value == "" {
		return "", false, nil
	}
	if !isCanonicalKitProfile(value) {
		return "", false, &Blocker{
			Code:                 "target-kit.invalid",
			Field:                "options.targetKitProfile",
			Message:              fmt.Sprintf("target KitProfile %q is not one of the three canonical profiles", raw),
			RequiredInputs:       []string{"targetKitProfile"},
			SuggestedKitProfiles: canonicalKitProfiles(),
		}
	}
	return value, true, nil
}

func normalizeContext(raw string, report *Report) (SiteKind, string, bool, *Blocker) {
	contextValue := strings.ToLower(strings.TrimSpace(raw))
	if contextValue == "" {
		return "", "", false, nil
	}

	report.Warnings = append(report.Warnings, Warning{
		Code:           "context.deprecated",
		Field:          "context",
		Message:        "stack-level context is a v1 compatibility input and is absent from canonical v2 output",
		RequiredAction: "review the separate Site and node hardware decisions",
	})

	switch contextValue {
	case string(models.ContextLocal):
		report.Decisions = append(report.Decisions, Decision{
			Code:   "context.local-to-home",
			Field:  "sites[0].kind",
			From:   contextValue,
			To:     string(SiteKindHome),
			Reason: "ADR-0029 compatibility mapping",
		})
		return SiteKindHome, "", true, nil
	case string(models.ContextCloud):
		report.Decisions = append(report.Decisions, Decision{
			Code:   "context.cloud-to-cloud",
			Field:  "sites[0].kind",
			From:   contextValue,
			To:     string(SiteKindCloud),
			Reason: "ADR-0029 compatibility mapping",
		})
		return SiteKindCloud, "", true, nil
	case string(models.ContextPi):
		report.Decisions = append(report.Decisions,
			Decision{
				Code:   "context.pi-to-home",
				Field:  "sites[0].kind",
				From:   contextValue,
				To:     string(SiteKindHome),
				Reason: "Pi is hardware, never locality; its legacy locality maps to home",
			},
			Decision{
				Code:   "context.pi-to-hardware-profile",
				Field:  "nodes[*].hardware.profile",
				From:   contextValue,
				To:     "pi",
				Reason: "ADR-0029 decomposes Pi hardware from Site locality",
			},
		)
		return SiteKindHome, "pi", true, nil
	default:
		return "", "", false, &Blocker{
			Code:           "context.unknown",
			Field:          "context",
			Message:        fmt.Sprintf("legacy context %q is not local, cloud, or pi", raw),
			RequiredInputs: []string{"context"},
		}
	}
}

func selectKitProfile(
	legacyKit string,
	target KitProfile,
	targetSet bool,
	contextSite SiteKind,
	contextSet bool,
	report *Report,
) (KitProfile, KitProfile, []Blocker) {
	var source KitProfile
	switch legacyKit {
	case "":
		if !targetSet {
			return "", "", []Blocker{{
				Code:                 "kit.missing",
				Field:                "stackkit",
				Message:              "context cannot select a product; a missing legacy StackKit requires an explicit target KitProfile",
				RequiredInputs:       []string{"targetKitProfile"},
				SuggestedKitProfiles: canonicalKitProfiles(),
			}}
		}
		report.Decisions = append(report.Decisions, Decision{
			Code:   "kit.explicit-target",
			Field:  "kit.slug",
			To:     string(target),
			Reason: "operator supplied targetKitProfile because the legacy product identity is absent",
		})
		return target, "", nil
	case "base-kit":
		source = KitProfileBasement
		report.Decisions = append(report.Decisions, Decision{
			Code:   "kit.base-alias",
			Field:  "kit.slug",
			From:   legacyKit,
			To:     string(source),
			Reason: "ADR-0029 defines base-kit as a Basement compatibility alias",
		})
	case string(KitProfileBasement), string(KitProfileCloud):
		source = KitProfile(legacyKit)
		report.Decisions = append(report.Decisions, Decision{
			Code:   "kit.identity-preserved",
			Field:  "kit.slug",
			From:   legacyKit,
			To:     legacyKit,
			Reason: "explicit Kit identity is authoritative and is never selected by context",
		})
	case string(KitProfileModern):
		return "", KitProfileModern, []Blocker{modernTopologyBlocker()}
	case LegacyHAKitSlug:
		blockers := []Blocker{}
		if !targetSet {
			blockers = append(blockers, Blocker{
				Code:                 "ha.target-required",
				Field:                "stackkit",
				Message:              "ha-kit is not a v2 KitProfile; migration requires an explicit target KitProfile",
				RequiredInputs:       []string{"targetKitProfile"},
				SuggestedKitProfiles: canonicalKitProfiles(),
			})
		} else {
			report.Decisions = append(report.Decisions, Decision{
				Code:   "ha.explicit-target",
				Field:  "kit.slug",
				From:   legacyKit,
				To:     string(target),
				Reason: "operator explicitly selected the product profile; context did not select it",
			})
		}
		blockers = append(blockers, Blocker{
			Code:           "ha.availability-contract-required",
			Field:          "addons.ha",
			Message:        "ha-kit cannot be normalized until the HA add-on is bound to explicit topology and availability objectives",
			RequiredInputs: []string{"availability.mode", "availability.rpoSeconds", "availability.rtoSeconds", "availability.fencing", "availability.failureDomainSpread", "nodes[*].failureDomain", "controlPlane.members"},
		})
		if target == KitProfileModern {
			blockers = append(blockers, modernTopologyBlocker())
		}
		return "", "", blockers
	default:
		return "", "", []Blocker{{
			Code:                 "kit.unknown",
			Field:                "stackkit",
			Message:              fmt.Sprintf("legacy StackKit %q is unknown and cannot be reinterpreted", legacyKit),
			RequiredInputs:       []string{"stackkit"},
			SuggestedKitProfiles: canonicalKitProfiles(),
		}}
	}

	if !targetSet || target == source {
		return source, source, nil
	}

	if source == KitProfileCloud && contextSet && contextSite == SiteKindHome {
		return "", source, []Blocker{{
			Code:                 "cloud.home-requires-modern",
			Field:                "options.targetKitProfile",
			Message:              "a Cloud Kit with home locality requires an explicit Modern conversion; it cannot be silently or directly relabeled as Basement",
			RequiredInputs:       modernRequiredInputs(),
			SuggestedKitProfiles: []KitProfile{KitProfileModern},
		}}
	}

	if target == KitProfileModern {
		return "", source, []Blocker{modernTopologyBlocker()}
	}

	// The only deterministic profile reclassification in the v1 shape is an
	// explicitly accepted Basement/base-kit + all-cloud context -> Cloud target.
	if source == KitProfileBasement && target == KitProfileCloud {
		report.Decisions = append(report.Decisions, Decision{
			Code:   "kit.explicit-target",
			Field:  "kit.slug",
			From:   string(source),
			To:     string(target),
			Reason: "operator explicitly selected Cloud for the legacy all-cloud Basement/base-kit ambiguity",
		})
		return target, source, nil
	}

	return "", source, []Blocker{{
		Code:                 "kit.reclassification-unsupported",
		Field:                "options.targetKitProfile",
		Message:              fmt.Sprintf("legacy KitProfile %q cannot be normalized directly to %q", source, target),
		RequiredInputs:       []string{"targetKitProfile"},
		SuggestedKitProfiles: []KitProfile{source},
	}}
}

func contextConflictBlocker(source, selected KitProfile, contextSite SiteKind, targetSet bool) Blocker {
	if source == KitProfileBasement && contextSite == SiteKindCloud && !targetSet {
		return Blocker{
			Code:                 "basement.cloud-target-required",
			Field:                "context",
			Message:              "Basement plus cloud locality is ambiguous; context may not overwrite the Basement identity",
			RequiredInputs:       []string{"targetKitProfile"},
			SuggestedKitProfiles: []KitProfile{KitProfileCloud, KitProfileModern},
		}
	}
	if source == KitProfileCloud && contextSite == SiteKindHome {
		return Blocker{
			Code:                 "cloud.home-requires-modern",
			Field:                "context",
			Message:              "Cloud plus home locality requires explicit Modern topology and bridge intent",
			RequiredInputs:       modernRequiredInputs(),
			SuggestedKitProfiles: []KitProfile{KitProfileModern},
		}
	}

	return Blocker{
		Code:           "context.kit-conflict",
		Field:          "context",
		Message:        fmt.Sprintf("context locality %q conflicts with explicitly selected KitProfile %q", contextSite, selected),
		RequiredInputs: []string{"context", "targetKitProfile"},
	}
}

func modernTopologyBlocker() Blocker {
	return Blocker{
		Code:                 "modern.topology-required",
		Field:                "stackkit",
		Message:              "legacy Modern Homelab cannot be normalized without explicit home/cloud Sites, home authority, per-node locality, and all five bridge contracts",
		RequiredInputs:       modernRequiredInputs(),
		SuggestedKitProfiles: []KitProfile{KitProfileModern},
	}
}

func modernRequiredInputs() []string {
	return []string{
		"sites[kind=home]",
		"sites[kind=cloud]",
		"nodes[*].siteRef",
		"controlPlane.authoritySiteRef=<home-site-id>",
		"bridge.overlay",
		"bridge.publications",
		"bridge.policy",
		"bridge.controlAgent",
		"partitionPolicy",
	}
}

func siteKindForProfile(profile KitProfile) SiteKind {
	if profile == KitProfileCloud {
		return SiteKindCloud
	}
	return SiteKindHome
}

func isCanonicalKitProfile(profile KitProfile) bool {
	switch profile {
	case KitProfileBasement, KitProfileCloud, KitProfileModern:
		return true
	default:
		return false
	}
}

func canonicalKitProfiles() []KitProfile {
	return []KitProfile{KitProfileBasement, KitProfileCloud, KitProfileModern}
}

func blocked(report Report, blockers ...Blocker) (NormalizedArchitecture, Report, error) {
	report.Status = MigrationStatusBlocked
	report.Blockers = append(report.Blockers, blockers...)
	return NormalizedArchitecture{}, report, &MigrationError{Blockers: append([]Blocker(nil), report.Blockers...)}
}
