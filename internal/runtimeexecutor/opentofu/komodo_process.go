package opentofu

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const komodoProcessRequestEnv = "STACKKIT_KOMODO_TOFU_REQUEST"
const komodoProcessCapabilityEnv = "STACKKIT_KOMODO_TOFU_CAPABILITY"

type komodoProcessRequest struct {
	Workspace string   `json:"workspace"`
	Project   string   `json:"project"`
	Binary    string   `json:"binary"`
	Args      []string `json:"args"`
}

type komodoProcessCustody struct {
	workspace, project string
	lease              *confinedfs.OutputLockLease
}

func (c komodoProcessCustody) OwnOpenTofuCommand(ctx context.Context, command *exec.Cmd) (*exec.Cmd, func(), error) {
	if err := requireKomodoProcessPlatform(); err != nil {
		return nil, nil, err
	}
	binary, err := filepath.Abs(command.Path)
	if err != nil {
		return nil, nil, err
	}
	request := komodoProcessRequest{c.workspace, c.project, binary, command.Args[1:]}
	if !request.valid() || command.Dir != request.root() {
		return nil, nil, errors.New("Komodo process custody refuses an unbounded OpenTofu command")
	}
	if c.lease == nil || c.lease.OutputRoot() != ".stackkit/runtime/applications/"+c.project {
		return nil, nil, errors.New("Komodo OpenTofu requires the exact held project lease")
	}
	owner := SecretExecBinary()
	if owner == "" {
		return nil, nil, errors.New("Komodo OpenTofu requires its installed process owner")
	}
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return nil, nil, err
	}
	capability := hex.EncodeToString(random)
	clear(random)
	server, err := newActionLeaseServer(c.lease, c.workspace, c.project, capability)
	if err != nil {
		return nil, nil, err
	}
	raw, _ := json.Marshal(request)
	owned := exec.CommandContext(ctx, owner, "komodo-tofu-owner")
	owned.Dir = command.Dir
	owned.Stdin = command.Stdin
	owned.Stdout = command.Stdout
	owned.Stderr = command.Stderr
	owned.Env = append(append([]string(nil), command.Env...), komodoProcessRequestEnv+"="+string(raw), komodoProcessCapabilityEnv+"="+capability, actionLeaseSocketEnv+"="+server.Endpoint())
	// Cancellation asks the custodian to reap its entire descendant tree. Killing
	// only this owner would drop its descriptor while providers could survive.
	configureKomodoOwnerCancellation(owned)
	return owned, server.Close, nil
}

func (r komodoProcessRequest) root() string {
	return filepath.Join(r.Workspace, ".stackkit/runtime/applications", r.Project, "opentofu")
}
func (r komodoProcessRequest) valid() bool {
	if !filepath.IsAbs(r.Workspace) || filepath.Clean(r.Workspace) != r.Workspace || !runtimeDirPattern.MatchString(r.Project) || !filepath.IsAbs(r.Binary) || filepath.Clean(r.Binary) != r.Binary || filepath.Base(r.Binary) != "tofu" {
		return false
	}
	a := r.Args
	if slices.Equal(a, []string{"init", "-input=false", "-lockfile=readonly"}) || slices.Equal(a, []string{"state", "list"}) || slices.Equal(a, []string{"show", "-json", PlanFile}) || slices.Equal(a, []string{"apply", "-input=false", PlanFile}) || slices.Equal(a, []string{"apply", "-input=false", "-suppress-forget-errors", PlanFile}) {
		return true
	}
	if slices.Equal(a, []string{"plan", "-input=false", "-detailed-exitcode", "-out=" + PlanFile}) || slices.Equal(a, []string{"plan", "-input=false", "-detailed-exitcode", "-destroy", "-out=" + PlanFile}) {
		return true
	}
	return len(a) == 3 && a[0] == "import" && a[1] == "komodo_stack.workload" && komodoStackIDPattern.MatchString(a[2])
}

// RunKomodoTofuOwner is an authenticated, closed subprocess of the pinned
// executor. No CLI arguments, user-selected command, shell, or extra engine.
func RunKomodoTofuOwner(ctx context.Context) (int, error) {
	if err := requireKomodoProcessPlatform(); err != nil {
		return 1, err
	}
	raw := os.Getenv(komodoProcessRequestEnv)
	if len(raw) == 0 || len(raw) > 8192 {
		return 1, errors.New("Komodo process owner requires its bounded parent request")
	}
	var request komodoProcessRequest
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || !request.valid() {
		return 1, errors.New("Komodo process owner refuses the command request")
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != request.root() {
		return 1, errors.New("Komodo process owner requires its exact workload root")
	}
	lease, err := receiveActionLease(request.Workspace, request.Project, os.Getenv(komodoProcessCapabilityEnv))
	if err != nil {
		return 1, err
	}
	defer func() {
		endpoint := os.Getenv(actionLeaseSocketEnv)
		lease.Close()
		// Accepted services are single-use. Their private directory may otherwise
		// remain after the application parent dies; never recursively remove it.
		directory := filepath.Dir(endpoint)
		if filepath.Dir(directory) == "/tmp" && strings.HasPrefix(filepath.Base(directory), "stackkit-lease-") && filepath.Base(endpoint) == "lease.sock" {
			if info, err := os.Lstat(directory); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm() == 0700 {
				os.Remove(endpoint)
				os.Remove(directory)
			}
		}
	}()
	marker, err := ReadRootMarker(cwd)
	if err != nil || !isKomodoProviderRoot(marker) || marker.ComposeProject != request.Project || marker.RuntimeDir != request.Project || marker.Kind != RootKindWorkload {
		return 1, errors.New("Komodo process owner has no exact provider root")
	}
	var intent komodoActionIntent
	if readKomodoRecord(request.Workspace, cwd, komodoActionFile, &intent) != nil || intent.Version != "stackkit.komodo-provider-action/v1" || !komodoProviderRootMatches(marker, intent.Deployment) || intent.Deployment.Manager != "komodo" || intent.Deployment.Deployment.Platform != "komodo" || request.Project != "stackkit-"+intent.Deployment.WorkloadRef+"-"+intent.Deployment.NodeRef || intent.Deployment.Deployment.AppName != request.Project || intent.Deployment.InstanceRef != marker.InstanceRef || localevidence.VerifyOwnerPolicyState(request.Workspace, intent.signingBytes(), intent.Signature) != nil {
		return 1, errors.New("Komodo process owner requires its signed execution intent")
	}
	config, err := architecturev2renderer.RenderKomodoWorkloadOpenTofu(architecturev2renderer.KomodoWorkloadSpec{ProjectName: request.Project, Directory: filepath.Dir(cwd), ServerID: intent.Deployment.ServerID, HeldID: intent.HeldID})
	actual, exists, readErr := readBoundedRegularFile(filepath.Join(cwd, ConfigFile), 1<<20)
	if err != nil || readErr != nil || !exists || !bytes.Equal(actual, config) {
		return 1, errors.New("Komodo process owner refuses a noncanonical root")
	}
	// Requests/capabilities are never passed as semantic inputs or persisted.
	os.Unsetenv(komodoProcessRequestEnv)
	os.Unsetenv(komodoProcessCapabilityEnv)
	return runKomodoOwnedProcess(ctx, request)
}
