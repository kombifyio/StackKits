package opentofu

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const runtipiActionFile = "runtipi-action.json"
const runtipiActionVersion = "stackkit.runtipi-api-action/v1"
const runtipiActionCapabilityEnv = "STACKKIT_RUNTIPI_ACTION_CAPABILITY"

type runtipiActionIntent struct {
	Version          string                                    `json:"version"`
	Request          nativehost.SelectedPaaSWorkloadDeployment `json:"request"`
	RequestDigest    string                                    `json:"requestDigest"`
	Held             platformdeploy.HostDeployment             `json:"held"`
	Phase            string                                    `json:"phase"`
	CapabilityDigest string                                    `json:"capabilityDigest"`
	Signature        localevidence.OwnerPolicyStateSignature   `json:"signature"`
}

func (i runtipiActionIntent) signingBytes() []byte {
	i.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(i)
	return append([]byte(runtipiActionVersion+"\n"), raw...)
}

func runtipiRequestDigest(d nativehost.SelectedPaaSWorkloadDeployment) string {
	raw, _ := json.Marshal(d)
	return digestBytes(raw)
}

func writeRuntipiIntent(workspace, root string, intent runtipiActionIntent) error {
	var err error
	intent.Signature, err = localevidence.SignOwnerPolicyState(workspace, intent.signingBytes())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	return writeFileAtomic(root, runtipiActionFile, raw, 0o600)
}

func readRuntipiIntent(workspace, root string) (runtipiActionIntent, error) {
	var intent runtipiActionIntent
	info, err := os.Lstat(filepath.Join(root, runtipiActionFile))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return intent, errors.New("Runtipi action requires its private bounded signed request")
	}
	raw, err := os.ReadFile(filepath.Join(root, runtipiActionFile))
	if err != nil {
		return intent, err
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&intent) != nil || decoder.Decode(new(any)) != io.EOF || intent.Version != runtipiActionVersion ||
		(intent.Phase != "prepared" && intent.Phase != "executing" && intent.Phase != "applied") ||
		intent.RequestDigest != runtipiRequestDigest(intent.Request) {
		return intent, errors.New("Runtipi action request is invalid")
	}
	if err := localevidence.VerifyOwnerPolicyState(workspace, intent.signingBytes(), intent.Signature); err != nil {
		return intent, errors.New("Runtipi action request signature is invalid")
	}
	return intent, nil
}

func newRuntipiCapability() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	value := hex.EncodeToString(raw[:])
	clear(raw[:])
	return value, digestBytes([]byte(value)), nil
}

// RunRuntipiWorkloadAction runs inside the parent Apply's project lease. A
// per-invocation capability admits the helper to a duplicate of the same OS
// lock. The helper keeps that descriptor through API and observation, including
// parent process death. No credential is an argument.
func RunRuntipiWorkloadAction(ctx context.Context, project, requestDigest string) error {
	if ctx == nil || !runtimeDirPattern.MatchString(project) || !validDigest(requestDigest) {
		return errors.New("invalid Runtipi API action")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	workspace := filepath.Clean(filepath.Join(root, "..", "..", "..", "..", ".."))
	relative, err := WorkloadRootRelativePath(project)
	if err != nil || root != filepath.Join(workspace, filepath.FromSlash(relative)) {
		return errors.New("Runtipi action is outside its exact workload root")
	}
	lease, err := receiveActionLease(workspace, project, os.Getenv(runtipiActionCapabilityEnv))
	if err != nil {
		return err
	}
	defer lease.Close()
	if err := requirePlainRelativeDirectory(workspace, relative); err != nil {
		return err
	}
	marker, err := ReadRootMarker(root)
	if err != nil || marker.Execution != RuntipiAPIExecution || marker.ModuleRef != runtipiFilesModule || marker.ComposeProject != project {
		return errors.New("Runtipi action requires its exact API-owned root")
	}
	intent, err := readRuntipiIntent(workspace, root)
	if err != nil {
		return err
	}
	capability := os.Getenv(runtipiActionCapabilityEnv)
	actual := sha256.Sum256([]byte(capability))
	if len(capability) != 64 || "sha256:"+hex.EncodeToString(actual[:]) != intent.CapabilityDigest || intent.Phase != "executing" || intent.RequestDigest != requestDigest ||
		marker.InstanceRef != intent.Request.InstanceRef || project != "stackkit-"+intent.Request.WorkloadRef+"-"+intent.Request.NodeRef {
		return errors.New("Runtipi action is not bound to the active parent Apply")
	}
	if err := validateRuntipiAPIRequest(workspace, intent.Request); err != nil {
		return err
	}
	if _, err := requireRuntipiHeldDeployment(workspace, project, intent.Request, &intent.Held); err != nil {
		return err
	}
	config, err := architecturev2renderer.RenderRuntipiWorkloadOpenTofu(project, requestDigest)
	if err != nil {
		return err
	}
	actualConfig, err := os.ReadFile(filepath.Join(root, ConfigFile))
	if err != nil || !bytes.Equal(actualConfig, config) {
		return errors.New("Runtipi root differs from the admitted operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	operations, err := nativehost.NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return err
	}
	if _, err = operations.ApplyWorkload(ctx, intent.Request); err != nil {
		return err
	}
	observation, err := operations.ObserveWorkload(ctx, intent.Request)
	if err != nil {
		return err
	}
	return nativehost.ValidateSelectedPaaSWorkloadObservation(intent.Request, observation)
}
