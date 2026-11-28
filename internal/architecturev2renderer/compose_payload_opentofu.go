package architecturev2renderer

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/engine"
)

// The Stage 1 OpenTofu wrapper of ADR-0045 is owned by internal/engine
// (ADR-0055). This file keeps the established names for every existing
// caller and translates engine errors into this package's stable error
// codes. See engine.RenderComposePayloadOpenTofu for the three-resource
// invariant.
const (
	// ComposePayloadLocalProviderVersion is the exact hashicorp/local release
	// every wrapper root pins, so offline packaging can mirror one provider.
	ComposePayloadLocalProviderVersion = engine.ComposePayloadLocalProviderVersion
	// ComposePayloadOpenTofuRequiredVersion admits the supported 1.12 line.
	ComposePayloadOpenTofuRequiredVersion = engine.ComposePayloadOpenTofuRequiredVersion
	// ComposePayloadWaitTimeoutSeconds bounds `docker compose up --wait`.
	ComposePayloadWaitTimeoutSeconds = engine.ComposePayloadWaitTimeoutSeconds
)

// composePayloadProjectPattern is the shared Compose project name rule.
var composePayloadProjectPattern = engine.ComposePayloadProjectPattern

// ComposePayloadSpec is the complete input of one Stage 1 wrapper root.
type ComposePayloadSpec = engine.ComposePayloadSpec

// RenderComposePayloadOpenTofu emits one OpenTofu root that writes the Compose
// payload to ../compose.yaml and applies it with Docker Compose.
func RenderComposePayloadOpenTofu(spec ComposePayloadSpec) ([]byte, error) {
	root, err := engine.RenderComposePayloadOpenTofu(spec)
	if err != nil {
		return nil, rendererError(err)
	}
	return root, nil
}

// ExtractComposePayload returns the Compose payload a Stage 1 wrapper root
// writes; see engine.ExtractComposePayload.
func ExtractComposePayload(mainTF []byte) ([]byte, error) {
	return engine.ExtractComposePayload(mainTF)
}

func escapeHCLTemplateLiteral(value string) string {
	return engine.EscapeHCLTemplateLiteral(value)
}

// rendererError keeps the renderer's stable error type for a failure the
// engine classified.
func rendererError(err error) error {
	var engineErr *engine.Error
	if err == nil || !errors.As(err, &engineErr) {
		return err
	}
	return &Error{Code: ErrorCode(engineErr.Code), Path: engineErr.Path, Message: engineErr.Message, Err: engineErr.Err}
}

// composePayloadOpenTofuRenderer renders a module's Compose artifact through
// the module's own Compose pipeline and wraps exactly those bytes.
type composePayloadOpenTofuRenderer struct {
	contract       RendererContract
	outputRef      string
	resourcePrefix string
	projectName    string
	compose        func(context.Context, RenderUnit) ([]byte, error)
}

func (r composePayloadOpenTofuRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	compose, err := r.compose(ctx, unit)
	if err != nil {
		return nil, err
	}
	root, err := RenderComposePayloadOpenTofu(ComposePayloadSpec{ResourcePrefix: r.resourcePrefix, ProjectName: r.projectName, Compose: compose})
	if err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: r.outputRef, Bytes: root}}, nil
}

var _ UnitRenderer = composePayloadOpenTofuRenderer{}
