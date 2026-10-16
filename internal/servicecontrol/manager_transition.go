package servicecontrol

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

type ManagerTransitionResult struct {
	APIVersion    string `json:"apiVersion"`
	OperationID   string `json:"operationId"`
	Project       string `json:"project"`
	From          string `json:"from"`
	To            string `json:"to"`
	Status        string `json:"status"`
	ExternalID    string `json:"externalId"`
	RecipeDigest  string `json:"recipeDigest"`
	RuntimeDigest string `json:"runtimeDigest"`
	EvidenceRef   string `json:"evidenceRef,omitempty"`
}

// InspectOwnedWorkloadManager reads the durable transfer outcome after an
// uncertain client response. It never repeats an API mutation.
func InspectOwnedWorkloadManager(workspace, workload string) (ManagerTransitionResult, error) {
	result := ManagerTransitionResult{APIVersion: "stackkit.manager-transition-result/v1"}
	if !contractIDPattern.MatchString(workload) {
		return result, errors.New("manager inspection requires an exact workload")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return result, err
	}
	result.Project = "stackkit-" + workload + "-" + owner.Binding.NodeRef
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, result.Project)
	if err != nil {
		return result, err
	}
	if state.WorkloadRef != workload || state.SiteRef != owner.Binding.SiteRef || state.NodeRef != owner.Binding.NodeRef {
		return result, errors.New("manager inspection refuses another node binding")
	}
	result.To, result.ExternalID, result.Status = platformdeploy.HostDeploymentManager(state), state.Deployment.ExternalID, "established"
	if h := state.Handover; h != nil {
		result.OperationID, result.From, result.To, result.Status = h.ID, h.From, h.To, h.Status
		result.RecipeDigest, result.RuntimeDigest = h.RecipeDigest, h.RuntimeDigest
	}
	return result, nil
}

// TransitionOwnedWorkloadManager uses the existing owner-bound node custody.
// It transfers API attachment, never application data or generated recipes.
// A retained application remains addressable even after an unrelated scoped
// plan became current; an unclaimed project is never implicitly adopted.
func TransitionOwnedWorkloadManager(ctx context.Context, workspace, workload, to, resume string, approved bool) (ManagerTransitionResult, error) {
	return transitionOwnedWorkloadManager(ctx, workspace, workload, to, resume, approved, observeManagerBindings)
}

func transitionOwnedWorkloadManager(ctx context.Context, workspace, workload, to, resume string, approved bool, observe func(context.Context, string, string) (string, string, error)) (ManagerTransitionResult, error) {
	return transitionOwnedWorkloadManagerBound(ctx, workspace, workload, to, resume, approved, observe, nil)
}

// TransitionAppliedWorkloadManager obtains fresh signed-Apply admission inside
// the same lifecycle lock that covers the attachment and custody mutation.
func TransitionAppliedWorkloadManager(ctx context.Context, workspace, workload, to, resume string, approved bool, bind func(platformdeploy.HostDeployment) (platformdeploy.HostManagerPlanBinding, error)) (ManagerTransitionResult, error) {
	if bind == nil {
		return ManagerTransitionResult{}, errors.New("manager transition requires verified applied-plan admission")
	}
	return transitionOwnedWorkloadManagerBound(ctx, workspace, workload, to, resume, approved, observeManagerBindings, bind)
}

func transitionOwnedWorkloadManagerBound(ctx context.Context, workspace, workload, to, resume string, approved bool, observe func(context.Context, string, string) (string, string, error), bind func(platformdeploy.HostDeployment) (platformdeploy.HostManagerPlanBinding, error)) (ManagerTransitionResult, error) {
	result := ManagerTransitionResult{APIVersion: "stackkit.manager-transition-result/v1", To: to}
	if ctx == nil || !approved {
		return result, errors.New("manager transition requires its context and explicit owner approval")
	}
	if !contractIDPattern.MatchString(workload) || (to != "komodo" && to != "standalone-compose") {
		return result, errors.New("manager transition requires an exact workload and an admitted manager")
	}
	err := lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		owner, err := localevidence.LoadOwnerCustody(workspace)
		if err != nil {
			return err
		}
		project := "stackkit-" + workload + "-" + owner.Binding.NodeRef
		result.Project = project
		state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return fmt.Errorf("manager transition requires existing node deployment custody: %w", err)
		}
		if state.WorkloadRef != workload || state.SiteRef != owner.Binding.SiteRef || state.NodeRef != owner.Binding.NodeRef {
			return errors.New("manager transition refuses another owner/site/node binding")
		}
		if h := state.Handover; h != nil && h.Status == "pending" && h.CoreProtocol != "" {
			return errors.New("native Core handover must be resumed through its exact native Core operation")
		}
		if h := state.Handover; h != nil && h.Status == "pending" && h.AttachmentState != "updating" {
			return errors.New("legacy pending manager attachment has no durable request qualification; keep both managers denied and reconcile the original upstream operation without replay")
		}
		if bind != nil {
			binding, err := bind(state)
			if err != nil {
				return err
			}
			if state.Handover != nil && state.Handover.Status == "pending" && (state.PlanBinding == nil || !reflect.DeepEqual(*state.PlanBinding, binding)) {
				return errors.New("pending manager transition requires its original applied-plan binding")
			}
			state.PlanBinding = &binding
		}
		cfg, err := platformdeploy.LoadOwnerKomodoConfigForServer(ctx, workspace, state.ServerID)
		if err != nil {
			return err
		}
		if state.Deployment.Platform != "komodo" || state.ServerID != cfg.ServerID {
			return errors.New("manager transition requires its existing exact Komodo node registration")
		}
		recipe, runtime, err := observe(ctx, workspace, project)
		if err != nil {
			return err
		}
		from := platformdeploy.HostDeploymentManager(state)
		directory := filepath.Join(workspace, ".stackkit", "runtime", "applications", project)
		adapter := platformdeploy.NewKomodoAdapter(cfg)
		initialWrite := false
		if h := state.Handover; h != nil && h.Status == "pending" {
			result.OperationID = h.ID
			if resume != h.ID || h.To != to || h.RecipeDigest != recipe || h.RuntimeDigest != runtime {
				return errors.New("incomplete manager handover requires its exact operation and unchanged recipe/runtime")
			}
			from = h.From
		} else {
			if resume != "" {
				return errors.New("requested manager handover is not pending")
			}
			if from == to {
				return errors.New("workload already belongs to the requested manager")
			}
			attachment, err := adapter.HostStackAttachment(ctx, state.Deployment, directory)
			if err != nil {
				return err
			}
			expected := ""
			if from == "komodo" {
				expected = cfg.ServerID
			}
			if attachment != expected {
				return errors.New("current manager custody differs from the exact live server attachment")
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return err
			}
			state.Manager = from
			state.Handover = &platformdeploy.HostManagerHandover{ID: "handover-" + hex.EncodeToString(nonce[:]), From: from, To: to, Status: "pending", RecipeDigest: recipe, RuntimeDigest: runtime, AttachmentState: "updating"}
			result.OperationID = state.Handover.ID
			if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
				return err
			}
			initialWrite = true
		}
		result.From, result.Status = from, "pending"
		result.ExternalID, result.RecipeDigest, result.RuntimeDigest = state.Deployment.ExternalID, recipe, runtime
		// The signed intent above grants this invocation one write. A resumed
		// caller only observes it, even if the original request has not committed.
		if initialWrite {
			if err := adapter.SetHostStackAttachment(ctx, state.Deployment, directory, to == "komodo"); err != nil {
				return err
			}
		}
		attachment, err := adapter.HostStackAttachment(ctx, state.Deployment, directory)
		if err != nil {
			return err
		}
		wanted := ""
		if to == "komodo" {
			wanted = cfg.ServerID
		}
		if attachment != wanted {
			return errors.New("original manager attachment has no positive upstream acknowledgement; keep this operation pending without another write")
		}
		afterRecipe, afterRuntime, err := observe(ctx, workspace, project)
		if err != nil {
			return err
		}
		if recipe != afterRecipe || runtime != afterRuntime {
			return errors.New("manager handover changed recipe, container, data mount or address bindings; custody remains pending")
		}
		state.Manager, state.Handover.Status = to, "succeeded"
		state.Handover.AttachmentState = "confirmed"
		if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, state); err != nil {
			return err
		}
		confirmed, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if err := platformdeploy.RequireHostDeploymentManager(confirmed, to); err != nil {
			return err
		}
		result.Status = "succeeded"
		result.EvidenceRef, err = (&Controller{workspace: workspace}).persistEvidence(confirmed)
		return err
	})
	if err != nil && result.Status == "pending" {
		return result, &Error{ReasonCode: "workload_manager_handover_pending", Retryable: true,
			Message: fmt.Sprintf("manager handover %s remains pending; resume only this operation with unchanged node bindings: %v", result.OperationID, err)}
	}
	return result, err
}

func managerDigest(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// observeManagerBindings reads only this project's private recipe and public
// Docker identity projection. Environment values and application data are not
// returned. The exact container/mount/address projection must stay unchanged.
func observeManagerBindings(ctx context.Context, workspace, project string) (string, string, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", "", err
	}
	defer transaction.Close()
	directory := filepath.Join(root.Name(), ".stackkit", "runtime", "applications", project)
	hashes := map[string]string{}
	var total int
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("manager handover refuses recipe symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if rel == "platform-deployment.json" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("manager handover refuses non-regular recipe files")
		}
		workspaceRel, err := filepath.Rel(root.Name(), path)
		if err != nil {
			return err
		}
		raw, _, err := transaction.ReadStableBounded(filepath.ToSlash(workspaceRel), 32<<20)
		if err != nil {
			return err
		}
		total += len(raw)
		if total > 32<<20 {
			clear(raw)
			return errors.New("manager handover recipe exceeds its read budget")
		}
		digest := sha256.Sum256(raw)
		clear(raw)
		hashes[filepath.ToSlash(rel)] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if hashes["compose.yaml"] == "" || hashes[".env"] == "" {
		return "", "", errors.New("manager handover requires the existing complete native recipe")
	}
	raw, err := managerNodeDocker(ctx, "ps", "--all", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.ID}}").Output()
	if err != nil {
		return "", "", errors.New("manager handover cannot observe its Docker project")
	}
	ids := strings.Fields(string(raw))
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", "", errors.New("manager handover refuses an absent project")
	}
	raw, err = managerNodeDocker(ctx, append([]string{"inspect"}, ids...)...).Output()
	if err != nil {
		return "", "", errors.New("manager handover cannot read its exact runtime bindings")
	}
	defer clear(raw)
	var containers []struct {
		ID    string `json:"Id"`
		Image string
		State struct {
			Running bool
			Paused  bool
		}
		Config struct{ Labels map[string]string }
		Mounts []struct {
			Type, Name, Source, Destination string
			RW                              bool
		}
		NetworkSettings struct {
			Ports    map[string][]struct{ HostIP, HostPort string }
			Networks map[string]struct{ IPAddress, GlobalIPv6Address, NetworkID string }
		}
	}
	if err := json.Unmarshal(raw, &containers); err != nil {
		return "", "", err
	}
	projection := make([]any, 0, len(containers))
	for _, c := range containers {
		if c.Config.Labels["com.docker.compose.project"] != project || c.Config.Labels["com.docker.compose.service"] == "" {
			return "", "", errors.New("manager handover refuses a container outside its exact project")
		}
		projection = append(projection, struct {
			ID, Image, Service string
			Running, Paused    bool
			Mounts             any
			Network            any
		}{c.ID, c.Image, c.Config.Labels["com.docker.compose.service"], c.State.Running, c.State.Paused, c.Mounts, c.NetworkSettings})
	}
	if err := root.VerifyPathIdentity(); err != nil {
		return "", "", err
	}
	return managerDigest(hashes), managerDigest(projection), nil
}

func managerNodeDocker(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "DOCKER_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}
