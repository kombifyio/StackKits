// Package contentbridge is the node side of the kombify Content Bridge
// (HOMELAB-CONTENT-BRIDGE-STANDARD, ADR-0047). It reads a closed set of
// counts from the self-hosted apps of one server, strictly read-only, and
// answers them as a `homelab-content/v1` document.
//
// The bridge is a separate component. It is not kombify Guard and never
// travels in Guard's channel. It answers only the consent tier its caller
// is granted, caches for at most one minute, stores nothing and never logs a
// payload.
package contentbridge

import "time"

// Version is the `version` of every document the bridge answers.
const Version = "homelab-content/v1"

// MaxCacheTTL is the longest the bridge keeps an app answer in memory
// (standard section 3).
const MaxCacheTTL = 60 * time.Second

// Tier is the consent tier a response may carry.
type Tier string

// The three consent tiers of the standard, in ascending order of content.
const (
	TierOff      Tier = "off"
	TierSummary  Tier = "summary"
	TierPreviews Tier = "previews"
)

// ParseTier accepts exactly the three tier names.
func ParseTier(value string) (Tier, bool) {
	switch tier := Tier(value); tier {
	case TierOff, TierSummary, TierPreviews:
		return tier, true
	default:
		return "", false
	}
}

func (t Tier) rank() int {
	switch t {
	case TierSummary:
		return 1
	case TierPreviews:
		return 2
	default:
		return 0
	}
}

// MinTier returns the lower of two tiers. The effective tier of an answer is
// the minimum of what the caller is granted and what the node can answer.
func MinTier(a, b Tier) Tier {
	if b.rank() < a.rank() {
		return b
	}
	return a
}

// UseCase names one content use case of the contract.
type UseCase string

// The use cases of homelab-content/v1.
const (
	UseCasePhotos    UseCase = "photos"
	UseCaseMedia     UseCase = "media"
	UseCaseDocuments UseCase = "documents"
	UseCaseFiles     UseCase = "files"
	UseCaseSmartHome UseCase = "smart_home"
)

// useCaseApps is the contract's fixed app per use case. Alternatives arrive
// as new adapters, never as another app name on an existing use case.
var useCaseApps = map[UseCase]string{
	UseCasePhotos:    "immich",
	UseCaseMedia:     "jellyfin",
	UseCaseDocuments: "paperless-ngx",
	UseCaseFiles:     "cloudreve",
	UseCaseSmartHome: "home-assistant",
}

// ParseUseCase accepts exactly the use cases of the contract.
func ParseUseCase(value string) (UseCase, bool) {
	useCase := UseCase(value)
	_, known := useCaseApps[useCase]
	return useCase, known
}

// Status is the per use case outcome. An app that cannot be read is never
// reported as empty.
type Status string

// The statuses of homelab-content/v1.
const (
	StatusOK                Status = "ok"
	StatusAppUnreachable    Status = "app_unreachable"
	StatusCredentialInvalid Status = "credential_invalid"
	StatusNotConfigured     Status = "not_configured"
	StatusConsentRequired   Status = "consent_required"
)

// Document is the `homelab-content/v1` response.
type Document struct {
	Version   string          `json:"version"`
	HomelabID string          `json:"homelab_id"`
	AsOf      string          `json:"as_of"`
	UseCases  []UseCaseResult `json:"use_cases"`
}

// UseCaseResult is one use case of a Document. Counts is present only for
// status ok. The bridge carries no items yet: titles and thumbnails belong to the
// previews tier, which the bridge cannot answer yet (see Ceiling).
type UseCaseResult struct {
	UseCase UseCase `json:"use_case"`
	App     string  `json:"app"`
	Status  Status  `json:"status"`
	Tier    Tier    `json:"tier"`
	AsOf    string  `json:"as_of,omitempty"`
	Counts  any     `json:"counts,omitempty"`
}

// PhotosCounts are the Photos counts of the summary tier. A pointer field is
// absent when the app did not answer it; zero is a real answer.
type PhotosCounts struct {
	Assets    *int64 `json:"assets,omitempty"`
	Images    *int64 `json:"images,omitempty"`
	Videos    *int64 `json:"videos,omitempty"`
	Albums    *int64 `json:"albums,omitempty"`
	OnThisDay *int64 `json:"on_this_day,omitempty"`
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// Identity of the bridge's own custody and app credentials. The workload
// reference follows the Photos add-on convention (photos-kiosk, photos-tools),
// so the custody reference reads secret://workloads/photos-content-bridge/
// immich-api-key like every governed workload secret.
const (
	WorkloadRef   = "photos-content-bridge"
	ImmichKeySlot = "immich-api-key"
	// ImmichKeyName names the key inside Immich, where the owner sees and
	// can revoke it. One key per bridge: issuing again replaces it.
	ImmichKeyName = "stackkits-content-bridge"
)

// ImmichKeySecretRef is the local custody reference of the bridge's Immich
// key. The key never leaves node custody (standard section 1).
func ImmichKeySecretRef() string {
	return "secret://workloads/" + WorkloadRef + "/" + ImmichKeySlot
}
