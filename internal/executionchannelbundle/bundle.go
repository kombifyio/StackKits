// Package executionchannelbundle verifies the short-lived, offline-authorized
// handoff into one exact StackKits execution channel. It owns no transport,
// credentials, provider lifecycle, discovery, retry, or execution behavior.
package executionchannelbundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	SchemaVersion  = "stackkit.execution-channel-bundle/v1"
	Kind           = "ExecutionChannelBundle"
	Audience       = "stackkit-runtime"
	MaxBundleBytes = 64 * 1024 * 1024
	MaxLifetime    = 5 * time.Minute
	MaxClockSkew   = 30 * time.Second
)

var issuerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type ChannelBinding struct {
	ChannelRef string `json:"channelRef"`
	SiteRef    string `json:"siteRef"`
	NodeRef    string `json:"nodeRef"`
}

type Envelope struct {
	SchemaVersion string                           `json:"schemaVersion"`
	Kind          string                           `json:"kind"`
	Audience      string                           `json:"audience"`
	IssuerID      string                           `json:"issuerId"`
	KeyID         string                           `json:"keyId"`
	IssuedAt      string                           `json:"issuedAt"`
	ExpiresAt     string                           `json:"expiresAt"`
	Channel       ChannelBinding                   `json:"channel"`
	Request       runtimeexecutor.ExecutionRequest `json:"request"`
	Signature     string                           `json:"signature,omitempty"`
}

type Verified struct {
	Request      runtimeexecutor.ExecutionRequest
	Channel      ChannelBinding
	IssuerID     string
	KeyID        string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	BundleDigest string
}

// DecodeAndVerify rejects a noncanonical, expired, substituted, unsealed, or
// cross-channel bundle before the caller constructs a mutating runtime. Trust
// is injected from the existing Owner-approved Advanced trust record.
func DecodeAndVerify(
	bundleRaw []byte,
	trust *advancedcapability.TrustBundle,
	now time.Time,
) (Verified, error) {
	var envelope Envelope
	if err := decodeCanonical(bundleRaw, MaxBundleBytes, &envelope); err != nil {
		return Verified{}, fmt.Errorf("decode execution-channel bundle: %w", err)
	}
	if envelope.SchemaVersion != SchemaVersion || envelope.Kind != Kind || envelope.Audience != Audience {
		return Verified{}, errors.New("execution-channel bundle has an unsupported contract")
	}
	if !issuerPattern.MatchString(envelope.IssuerID) {
		return Verified{}, errors.New("execution-channel bundle issuer is invalid")
	}
	issuedAt, err := canonicalTime(envelope.IssuedAt)
	if err != nil {
		return Verified{}, fmt.Errorf("execution-channel bundle issuedAt: %w", err)
	}
	expiresAt, err := canonicalTime(envelope.ExpiresAt)
	if err != nil {
		return Verified{}, fmt.Errorf("execution-channel bundle expiresAt: %w", err)
	}
	now = now.UTC()
	if issuedAt.After(now.Add(MaxClockSkew)) || !expiresAt.After(now) ||
		!expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > MaxLifetime {
		return Verified{}, errors.New("execution-channel bundle is outside its allowed validity window")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		base64.RawStdEncoding.EncodeToString(signature) != envelope.Signature {
		return Verified{}, errors.New("execution-channel bundle signature is invalid")
	}
	unsigned := envelope
	unsigned.Signature = ""
	canonicalUnsigned, err := resolvedplan.CanonicalJSON(unsigned)
	if err != nil {
		return Verified{}, fmt.Errorf("canonicalize execution-channel bundle payload: %w", err)
	}
	payloadDigest := sha256.Sum256(append([]byte(SchemaVersion+"\x00"), canonicalUnsigned...))
	if err := advancedcapability.VerifyTrustedDigest(
		trust, envelope.IssuerID, envelope.KeyID, payloadDigest[:], signature,
	); err != nil {
		return Verified{}, fmt.Errorf("execution-channel bundle trust verification failed: %w", err)
	}
	sealedRequest, err := runtimeexecutor.SealRequest(envelope.Request)
	if err != nil {
		return Verified{}, fmt.Errorf("execution-channel bundle request is not sealed: %w", err)
	}
	if sealedRequest.RequestDigest != envelope.Request.RequestDigest ||
		sealedRequest.ArtifactSetHash != envelope.Request.ArtifactSetHash {
		return Verified{}, errors.New("execution-channel bundle request digests are not sealed")
	}
	envelope.Request = sealedRequest
	channelRequest := runtimeexecutor.ExecutionChannelRequest{
		ChannelRef: envelope.Channel.ChannelRef,
		SiteRef:    envelope.Channel.SiteRef,
		NodeRef:    envelope.Channel.NodeRef,
		RuntimeTargets: runtimeexecutor.CloneExecutionRequest(
			envelope.Request,
		).RuntimeTargets,
		HealthTargets: runtimeexecutor.CloneExecutionRequest(
			envelope.Request,
		).HealthTargets,
	}
	if err := channelRequest.Validate(); err != nil {
		return Verified{}, fmt.Errorf("execution-channel bundle scope is invalid: %w", err)
	}
	canonicalEnvelope, err := resolvedplan.CanonicalJSON(envelope)
	if err != nil {
		return Verified{}, fmt.Errorf("canonicalize execution-channel bundle: %w", err)
	}
	digest := sha256.Sum256(canonicalEnvelope)
	return Verified{
		Request:      runtimeexecutor.CloneExecutionRequest(envelope.Request),
		Channel:      envelope.Channel,
		IssuerID:     envelope.IssuerID,
		KeyID:        envelope.KeyID,
		IssuedAt:     issuedAt,
		ExpiresAt:    expiresAt,
		BundleDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func decodeCanonical(raw []byte, limit int64, destination any) error {
	if len(raw) == 0 || int64(len(raw)) > limit {
		return errors.New("document size is outside the allowed bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains multiple JSON values")
		}
		return err
	}
	canonical, err := resolvedplan.CanonicalJSON(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return errors.New("document is not canonical JSON")
	}
	return nil
}

func canonicalTime(value string) (time.Time, error) {
	if value != strings.TrimSpace(value) || value == "" {
		return time.Time{}, errors.New("timestamp is not canonical")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339) != value {
		return time.Time{}, errors.New("timestamp must be RFC3339 UTC with second precision")
	}
	return parsed, nil
}
