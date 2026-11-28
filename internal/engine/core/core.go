// Package core is the canonical Core render boundary of ADR-0055: one typed
// entrypoint for the Home (Basement, Basement Lite) and Cloud (Cloud,
// Cloud Standalone) Core owners. In Phase 1 it delegates to the established
// Core renderers in internal/architecturev2renderer, whose callers keep
// working unchanged; Phase 2 moves the renderer bodies here and deletes the
// legacy entrypoints.
package core

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/engine"
)

// Kind is a Core owner module.
type Kind string

const (
	Basement        Kind = "stackkits-basement-core-runtime"
	BasementLite    Kind = "stackkits-basement-core-lite-runtime"
	Cloud           Kind = "stackkits-cloud-core-runtime"
	CloudStandalone Kind = "stackkits-cloud-core-standalone-runtime"
)

// Kinds lists every Core owner.
func Kinds() []Kind { return []Kind{Basement, BasementLite, Cloud, CloudStandalone} }

// ParseKind admits a Core owner module reference.
func ParseKind(moduleRef string) (Kind, error) {
	for _, kind := range Kinds() {
		if string(kind) == moduleRef {
			return kind, nil
		}
	}
	return "", fmt.Errorf("core: %q is not a Core owner", moduleRef)
}

// Address is the identity a Core Compose artifact is rendered for. Prefix
// and OriginCertificate apply only to the Cloud Cores (a kombify.me managed
// address and its delivered Origin CA certificate, ADR-0047).
type Address struct {
	Domain            string
	Prefix            string
	OriginCertificate bool
}

// Contracts are the renderer contracts of one Core's Compose artifact and
// its OpenTofu and Terramate twins.
type Contracts struct {
	Compose   architecturev2renderer.RendererContract
	OpenTofu  architecturev2renderer.RendererContract
	Terramate architecturev2renderer.RendererContract
}

// RenderCompose renders the Core Compose artifact for an address. It fails
// closed where the legacy renderer returns nothing (an inadmissible domain)
// and for an address form the Core does not support.
func RenderCompose(kind Kind, address Address) ([]byte, error) {
	var compose []byte
	switch kind {
	case Basement, BasementLite:
		if address.Prefix != "" || address.OriginCertificate {
			return nil, fmt.Errorf("core: %s renders only a domain address", kind)
		}
		if kind == Basement {
			compose = architecturev2renderer.RenderBasementCoreComposeForDomain(address.Domain)
		} else {
			compose = architecturev2renderer.RenderBasementCoreLiteComposeForDomain(address.Domain)
		}
	case Cloud:
		switch {
		case address.OriginCertificate:
			compose = architecturev2renderer.RenderCloudCoreComposeForOriginCertificate(address.Domain, address.Prefix)
		case address.Prefix != "":
			compose = architecturev2renderer.RenderCloudCoreComposeForAddress(address.Domain, address.Prefix)
		default:
			compose = architecturev2renderer.RenderCloudCoreComposeForDomain(address.Domain)
		}
	case CloudStandalone:
		switch {
		case address.OriginCertificate:
			compose = architecturev2renderer.RenderCloudStandaloneCoreComposeForOriginCertificate(address.Domain, address.Prefix)
		case address.Prefix != "":
			compose = architecturev2renderer.RenderCloudStandaloneCoreComposeForAddress(address.Domain, address.Prefix)
		default:
			compose = architecturev2renderer.RenderCloudStandaloneCoreComposeForDomain(address.Domain)
		}
	default:
		return nil, fmt.Errorf("core: %q is not a Core owner", kind)
	}
	if len(compose) == 0 {
		return nil, fmt.Errorf("core: %s rejects address %q", kind, address.Domain)
	}
	return compose, nil
}

// ExpectedCompose is the default-address artifact the local runtime-owner
// admission compares with.
func ExpectedCompose(kind Kind) ([]byte, error) {
	switch kind {
	case Basement:
		return architecturev2renderer.ExpectedBasementCoreComposeArtifact(), nil
	case Cloud:
		return architecturev2renderer.ExpectedCloudCoreComposeArtifact(), nil
	case CloudStandalone:
		return architecturev2renderer.ExpectedCloudStandaloneCoreComposeArtifact(), nil
	default:
		return nil, fmt.Errorf("core: %s has no default-address artifact", kind)
	}
}

// ValidateCompose reports whether content is an artifact the Core renders.
func ValidateCompose(kind Kind, content []byte) (bool, error) {
	switch kind {
	case Basement:
		return architecturev2renderer.ValidateBasementCoreComposeArtifact(content), nil
	case BasementLite:
		return architecturev2renderer.ValidateBasementCoreLiteComposeArtifact(content), nil
	case Cloud:
		return architecturev2renderer.ValidateCloudCoreComposeArtifact(content), nil
	case CloudStandalone:
		return architecturev2renderer.ValidateCloudStandaloneCoreComposeArtifact(content), nil
	default:
		return false, fmt.Errorf("core: %q is not a Core owner", kind)
	}
}

// ServiceContracts are the readiness contracts of the Core's services.
func ServiceContracts(kind Kind) ([]architecturev2renderer.BasementCoreServiceContract, error) {
	switch kind {
	case Basement:
		return architecturev2renderer.BasementCoreServiceContracts(), nil
	case BasementLite:
		return architecturev2renderer.BasementCoreLiteServiceContracts(), nil
	case Cloud:
		return architecturev2renderer.CloudCoreServiceContracts(), nil
	case CloudStandalone:
		return architecturev2renderer.CloudStandaloneCoreServiceContracts(), nil
	default:
		return nil, fmt.Errorf("core: %q is not a Core owner", kind)
	}
}

// RendererContracts are the Core's registered renderer contracts.
func RendererContracts(kind Kind) (Contracts, error) {
	switch kind {
	case Basement:
		return Contracts{
			Compose:   architecturev2renderer.BasementCoreComposeRendererContract(),
			OpenTofu:  architecturev2renderer.BasementCoreOpenTofuRendererContract(),
			Terramate: architecturev2renderer.BasementCoreTerramateRendererContract(),
		}, nil
	case BasementLite:
		return Contracts{
			Compose:   architecturev2renderer.BasementCoreLiteComposeRendererContract(),
			OpenTofu:  architecturev2renderer.BasementCoreLiteOpenTofuRendererContract(),
			Terramate: architecturev2renderer.BasementCoreLiteTerramateRendererContract(),
		}, nil
	case Cloud:
		return Contracts{
			Compose:   architecturev2renderer.CloudCoreComposeRendererContract(),
			OpenTofu:  architecturev2renderer.CloudCoreOpenTofuRendererContract(),
			Terramate: architecturev2renderer.CloudCoreTerramateRendererContract(),
		}, nil
	case CloudStandalone:
		return Contracts{
			Compose:   architecturev2renderer.CloudStandaloneCoreComposeRendererContract(),
			OpenTofu:  architecturev2renderer.CloudStandaloneCoreOpenTofuRendererContract(),
			Terramate: architecturev2renderer.CloudStandaloneCoreTerramateRendererContract(),
		}, nil
	default:
		return Contracts{}, fmt.Errorf("core: %q is not a Core owner", kind)
	}
}

// RoutingNetwork is the external network the Core provides to workload
// routes; HomeCore reports a Core with separate LAN and remote listeners.
func RoutingNetwork(kind Kind) (string, error) { return engine.CoreRoutingNetwork(string(kind)) }

// HomeCore reports a Core with separate LAN and remote listeners.
func HomeCore(kind Kind) bool { return engine.HomeCore(string(kind)) }
