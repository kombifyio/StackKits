package localevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/addressplan"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const CloudSessionRoutingPath = ".stackkit/custody/cloud-session-routing.json"
const CloudSessionOverlayPath = ".stackkit/runtime/cloud-core-standalone/session-routing.yaml"
const CloudSessionSourcePath = ".stackkit/runtime/cloud-core-standalone/compose.yaml"

type CloudSessionContainer struct {
	Service       string `json:"service"`
	ID            string `json:"id"`
	ImageID       string `json:"imageId"`
	ConfigHash    string `json:"configHash"`
	MountsDigest  string `json:"mountsDigest"`
	RuntimeDigest string `json:"runtimeDigest"`
	RoutingDigest string `json:"routingDigest"`
}

// CloudSessionRouting authenticates a public-only derivative of canonical
// intent. Original runtime custody and OIDC credential records stay immutable.
type CloudSessionRouting struct {
	Version             string                          `json:"version"`
	Phase               string                          `json:"phase"`
	SpecPath            string                          `json:"specPath"`
	BeforeSpec          []byte                          `json:"beforeSpec"`
	AfterSpec           []byte                          `json:"afterSpec"`
	Before              addressplan.SessionTopology     `json:"before"`
	After               addressplan.SessionTopology     `json:"after"`
	CloudDigest         string                          `json:"cloudCustodyDigest"`
	BindingDigest       string                          `json:"tinyAuthBindingDigest"`
	OwnerDigest         string                          `json:"ownerBindingDigest"`
	ClientPolicyDigest  string                          `json:"clientPolicyDigest"`
	OwnerPasskeysDigest string                          `json:"ownerPasskeysDigest"`
	SourceDigest        string                          `json:"sourceComposeDigest"`
	OverlayDigest       string                          `json:"overlayDigest"`
	Containers          []CloudSessionContainer         `json:"containers"`
	Migration           *CloudSessionComponentMigration `json:"componentMigration,omitempty"`
	Signature           OwnerPolicyStateSignature       `json:"signature"`
}

func SessionRoutingDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func SessionRoutingValueDigest(value any) string {
	raw, _ := json.Marshal(value)
	return SessionRoutingDigest(raw)
}

func ReadCloudSessionFile(workspace, path string) ([]byte, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStableBounded(path, 8<<20)
	return raw, err
}

func WriteCloudSessionFile(workspace, path string, raw []byte) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(path, raw)
	if err != nil || !result.Installed || !result.FileSynced {
		return errors.New("cloud_session_transition_write_failed: private atomic state write did not complete")
	}
	return nil
}

func SaveCloudSessionRouting(workspace string, state CloudSessionRouting) error {
	state.Version = "stackkit.cloud-session-routing/v1"
	if state.Migration != nil {
		state.Version = "stackkit.cloud-session-routing/v2"
		if err := state.Migration.Validate(); err != nil {
			return err
		}
	}
	state.Signature = OwnerPolicyStateSignature{}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	state.Signature, err = SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return WriteCloudSessionFile(workspace, CloudSessionRoutingPath, raw)
}

func LoadCloudSessionRouting(workspace string) (result CloudSessionRouting, returnErr error) {
	defer func() { returnErr = CloudSessionRoutingDiagnostic(returnErr) }()
	var state CloudSessionRouting
	raw, err := ReadCloudSessionFile(workspace, CloudSessionRoutingPath)
	if err != nil {
		return state, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, errors.New("cloud_session_transition_invalid: malformed transition")
	}
	canonical, err := json.Marshal(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return state, errors.New("cloud_session_transition_invalid: transition journal is not canonical")
	}
	signature := state.Signature
	state.Signature = OwnerPolicyStateSignature{}
	payload, err := json.Marshal(state)
	state.Signature = signature
	if err != nil || VerifyOwnerPolicyState(workspace, payload, signature) != nil {
		return state, errors.New("cloud_session_transition_invalid: Owner signature does not verify")
	}
	if (state.Version != "stackkit.cloud-session-routing/v1" && state.Version != "stackkit.cloud-session-routing/v2") ||
		(state.Phase != "prepared" && state.Phase != "callback-bound" && state.Phase != "runtime-bound" && state.Phase != "active" && state.Phase != "aborting" && state.Phase != "aborted") {
		return state, errors.New("cloud_session_transition_invalid: unsupported transition phase")
	}
	if (state.Version == "stackkit.cloud-session-routing/v2") != (state.Migration != nil) {
		return state, errors.New("cloud_session_transition_invalid: component migration version differs")
	}
	if state.Migration != nil {
		if err := state.Migration.Validate(); err != nil {
			return state, err
		}
	}
	before, beforeErr := addressplan.SessionTopologyFromCanonical(state.BeforeSpec)
	after, afterErr := addressplan.SessionTopologyFromCanonical(state.AfterSpec)
	if beforeErr != nil || afterErr != nil || before != state.Before || after != state.After || after.ValidateTransitionFrom(before) != nil {
		return state, errors.New("cloud_session_transition_invalid: canonical route bindings differ")
	}
	cloud, err := LoadCloudRuntimeCustody(workspace)
	if err != nil || SessionRoutingValueDigest(cloud) != state.CloudDigest || cloud.IdentityAddress().PocketIDOrigin() != "https://"+before.PocketIDHost {
		return state, errors.New("cloud_session_transition_drift: original Cloud custody differs")
	}
	binding, err := LoadBasementTinyAuthPocketIDBinding(workspace)
	if err != nil || SessionRoutingValueDigest(binding) != state.BindingDigest || binding.CallbackURL != before.CallbackURL() {
		return state, errors.New("cloud_session_transition_drift: original TinyAuth credential binding differs")
	}
	owner, err := LoadOwnerRuntimeBinding(workspace)
	if err != nil || OwnerRuntimeBindingDigest(owner) != state.OwnerDigest {
		return state, errors.New("cloud_session_transition_drift: original Owner binding differs")
	}
	for path, digest := range map[string]string{CloudSessionSourcePath: state.SourceDigest, CloudSessionOverlayPath: state.OverlayDigest} {
		data, err := ReadCloudSessionFile(workspace, path)
		if err != nil || SessionRoutingDigest(data) != digest {
			return state, fmt.Errorf("cloud_session_transition_drift: retained runtime file differs: %s", path)
		}
	}
	current, err := ReadCloudSessionFile(workspace, state.SpecPath)
	if err != nil || (!bytes.Equal(current, state.BeforeSpec) && !bytes.Equal(current, state.AfterSpec)) ||
		(state.Phase == "active" && !bytes.Equal(current, state.AfterSpec)) || (state.Phase == "aborted" && !bytes.Equal(current, state.BeforeSpec)) {
		return state, errors.New("cloud_session_transition_drift: StackSpec differs from the signed transition")
	}
	return state, nil
}

// EffectiveTinyAuthCallbackURL keeps the raw credential verifier independent
// of the transition. A pending transition always requires explicit resume.
func EffectiveTinyAuthCallbackURL(workspace string) (result string, returnErr error) {
	defer func() { returnErr = CloudSessionRoutingDiagnostic(returnErr) }()
	state, err := LoadCloudSessionRouting(workspace)
	if errors.Is(err, os.ErrNotExist) || (err == nil && state.Phase == "aborted") {
		address, err := LocalIdentityRuntimeAddress(workspace)
		if err != nil {
			return "", err
		}
		return TinyAuthPocketIDCallbackURL(address), nil
	}
	if err != nil {
		return "", err
	}
	if state.Phase != "active" {
		return "", errors.New("cloud_session_transition_pending: resume stackkit address session-zone with the original zone")
	}
	return state.After.CallbackURL(), nil
}
