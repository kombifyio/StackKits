// Package ownapplication records node custody for Techstack-owned application
// intent. Catalog and own workloads share the native Compose executor.
package ownapplication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/ownappfirewall"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const statePath = ".stackkit/own-applications/state.json"
const stateVersion = "stackkit.own-application-custody/v1"

type Record struct {
	TransitionFrom        *contract.Bundle `json:"transitionFrom,omitempty"`
	SecretsKept           bool             `json:"secretsKept,omitempty"`
	Generation            string           `json:"generation"`
	Bundle                contract.Bundle  `json:"bundle"`
	PreviousImage         string           `json:"previousImage,omitempty"`
	PreviousResolvedImage string           `json:"previousResolvedImage,omitempty"`
	// PreviousImages and PreviousResolvedImages carry the rollback custody of a
	// project, keyed by service name.
	PreviousImages         map[string]string `json:"previousImages,omitempty"`
	PreviousResolvedImages map[string]string `json:"previousResolvedImages,omitempty"`
	State                  string            `json:"state"`
	// Checkpoint is the data checkpoint of the latest tag update; its retained
	// copies stay until a later checkpoint or a data-deleting removal.
	Checkpoint *DataCheckpoint `json:"checkpoint,omitempty"`
	Revision   string          `json:"revision"`
	Operation  Operation       `json:"operation"`
}
type Operation struct {
	Firewall       *ownappfirewall.Receipt `json:"firewall,omitempty"`
	FirewallAction string                  `json:"firewallAction,omitempty"`
	ID             string                  `json:"id"`
	Action         string                  `json:"action"`
	RequestDigest  string                  `json:"requestDigest"`
	ReviewDigest   string                  `json:"reviewDigest"`
	Status         string                  `json:"status"`
	At             time.Time               `json:"at"`
}
type State struct {
	APIVersion   string                                        `json:"apiVersion"`
	Applications map[string]Record                             `json:"applications"`
	Signature    localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}
type Request struct {
	Action           string                `json:"action"`
	Name             string                `json:"name"`
	Application      *contract.Application `json:"application,omitempty"`
	KeepSecrets      bool                  `json:"keepSecrets,omitempty"`
	ResolvedImage    string                `json:"resolvedImage,omitempty"`
	ResolvedImages   map[string]string     `json:"resolvedImages,omitempty"`
	SecretMaterial   map[string]string     `json:"-"`
	SecretCommitment string                `json:"secretCommitment,omitempty"`
	DeleteData       bool                  `json:"deleteData,omitempty"`
	OperationID      string                `json:"operationId,omitempty"`
	ReviewDigest     string                `json:"reviewDigest,omitempty"`
	DryRun           bool                  `json:"dryRun,omitempty"`
	OwnerApproved    bool                  `json:"-"`
	// ImageTag and AllowMajor name a tag update (UpdateImage); Checkpoint names
	// the data checkpoint a rollback restores (RollbackWithCheckpoint). Each is
	// part of the request and so of the review digest.
	ImageTag   string `json:"imageTag,omitempty"`
	AllowMajor bool   `json:"allowMajor,omitempty"`
	Checkpoint string `json:"checkpoint,omitempty"`
}
type Result struct {
	FirewallRulesetDigest string                 `json:"firewallRulesetDigest,omitempty"`
	FirewallPolicyDigest  string                 `json:"firewallPolicyDigest,omitempty"`
	APIVersion            string                 `json:"apiVersion"`
	NetworkNotice         string                 `json:"networkNotice"`
	Action                string                 `json:"action"`
	DryRun                bool                   `json:"dryRun"`
	ReviewDigest          string                 `json:"reviewDigest,omitempty"`
	Record                Record                 `json:"record"`
	Compose               string                 `json:"compose,omitempty"`
	Route                 *accessgatetable.Route `json:"route,omitempty"`
	BackupVolumes         []string               `json:"backupVolumes"`
	Observation           json.RawMessage        `json:"observation,omitempty"`
	ObservationError      string                 `json:"observationError,omitempty"`
	// Outcome is the stable outcome code of this call: reviewed (dry run or
	// render), observed or observation_unavailable (status, logs), and for an
	// executed mutation created, updated, rolled_back, started, stopped,
	// restarted or removed. A refused, pending or denied call carries that
	// outcome with a stable reasonCode and the message in reason (OutcomeFor).
	Outcome    string `json:"outcome,omitempty"`
	ReasonCode string `json:"reasonCode,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ErrPending marks a mutation whose runtime step failed after its signed
// write-ahead custody: its outcome is uncertain until status is inspected and a
// new reviewed operation reconciles it.
var ErrPending = errors.New("own application operation pending")

// executedOutcome names the outcome code of a completed mutation.
func executedOutcome(action string) string {
	switch action {
	case "create":
		return "created"
	case "update":
		return "updated"
	case "rollback":
		return "rolled_back"
	case "start":
		return "started"
	case "stop":
		return "stopped"
	case "restart":
		return "restarted"
	case "remove":
		return "removed"
	}
	return "observed"
}

// OutcomeFor is the result a failed call reports: pending (reasonCode
// operation_pending) when signed custody already records the mutation as
// started, refused with the given reason code when nothing changed.
func OutcomeFor(action, code string, err error) Result {
	outcome := "refused"
	if errors.Is(err, ErrPending) {
		outcome, code = "pending", ReasonPending
	}
	return Result{APIVersion: "stackkit.own-application-result/v1", Action: action, Outcome: outcome, ReasonCode: code, Reason: err.Error(), BackupVolumes: []string{}}
}

func hash(v any) string {
	raw, _ := json.Marshal(v)
	s := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(s[:])
}
func signingBytes(s State) []byte {
	s.Signature = localevidence.OwnerLifecycleMutationSignature{}
	raw, _ := json.Marshal(s)
	return raw
}
func loadSigned(workspace string) (State, error) {
	empty := State{APIVersion: stateVersion, Applications: map[string]Record{}}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	t, err := root.BeginTransaction()
	if err != nil {
		return empty, err
	}
	defer t.Close()
	raw, _, err := t.ReadStableBounded(statePath, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var s State
	if json.Unmarshal(raw, &s) != nil || s.APIVersion != stateVersion || s.Applications == nil {
		return empty, errors.New("own application custody is malformed")
	}
	authority, err := localevidence.LoadHomeAuthorityPublic(workspace)
	if err != nil {
		return empty, err
	}
	if err = authority.VerifyLifecycleMutation(signingBytes(s), s.Signature); err != nil {
		return empty, err
	}
	return s, nil
}
func validateRecord(name string, r Record) error {
	if r.TransitionFrom != nil && (r.Operation.Status != "pending" || r.TransitionFrom.Validate() != nil || r.TransitionFrom.Application.Name != name || r.TransitionFrom.Installation.SiteRef != r.Bundle.Installation.SiteRef || r.TransitionFrom.Installation.NodeRef != r.Bundle.Installation.NodeRef) {
		return errors.New("invalid own application network transition custody")
	}

	if r.Operation.Status != "pending" && r.Operation.Status != "succeeded" {
		return errors.New("invalid own application operation state")
	}
	switch r.State {
	case "", "running", "stopped", "removed":
	default:
		return errors.New("invalid own application state")
	}
	if r.PreviousResolvedImage != "" && !r.Bundle.Application.IsProject() {
		prior := r.Bundle
		prior.Application.Image = r.PreviousImage
		prior.ResolvedImage = r.PreviousResolvedImage
		if prior.Validate() != nil {
			return errors.New("invalid previous image custody")
		}
	}
	if len(r.PreviousResolvedImages) != 0 && r.Bundle.Application.IsProject() {
		prior := r.Bundle
		services := append([]contract.Service(nil), prior.Application.Services...)
		for i := range services {
			previous, ok := r.PreviousImages[services[i].Name]
			if !ok {
				return errors.New("invalid previous image custody")
			}
			services[i].Image = previous
		}
		prior.Application.Services = services
		prior.ResolvedImages = r.PreviousResolvedImages
		prior.ResolvedImage = ""
		prior.ImageID = ""
		prior.ImageIDs = nil
		if prior.Validate() != nil {
			return errors.New("invalid previous image custody")
		}
	}
	if name != r.Bundle.Application.Name || r.Bundle.Validate() != nil || !r.Bundle.ImagesResolved() || r.Revision != hash(r.Bundle) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(r.Generation) {
		return errors.New("own application custody differs from its admitted bundle")
	}
	return nil
}
func Load(workspace string) (State, error) {
	s, e := loadSigned(workspace)
	if e != nil {
		return s, e
	}
	for name, r := range s.Applications {
		if e = validateRecord(name, r); e != nil {
			return State{}, e
		}
	}
	return s, nil
}

func save(workspace string, s State) error {
	sig, err := localevidence.SignOwnerLifecycleMutation(workspace, signingBytes(s))
	if err != nil {
		return err
	}
	s.Signature = sig
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("own application custody exceeds its bound")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	t, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err = t.MkdirAll(".stackkit/own-applications", 0700); err != nil {
		t.Close()
		return err
	}
	t.Close()
	view, err := root.View("")
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(statePath, raw)
	return err
}
func Mutating(action string) bool {
	return action == "create" || action == "update" || action == "rollback" || action == "start" || action == "stop" || action == "restart" || action == "remove"
}
func Execute(ctx context.Context, workspace string, installation contract.Installation, req Request) (Result, error) {
	if !contract.ValidName(req.Name) {
		return Result{}, errors.New("valid own application name is required")
	}
	switch req.Action {
	case "create", "update", "rollback", "start", "stop", "restart", "remove", "status", "logs", "render":
	default:
		return Result{}, errors.New("action must be create, update, rollback, start, stop, restart, remove, status, logs or render")
	}
	if req.KeepSecrets && (req.Action != "remove" || req.DeleteData) {
		return Result{}, errors.New("keep-secrets requires remove with retained data")
	}
	if req.SecretMaterial != nil {
		if req.Action != "create" && req.Action != "update" {
			return Result{}, errors.New("secret input is only admitted on create/update")
		}
		if req.Application == nil {
			return Result{}, errors.New("secret input needs its own application document")
		}
		for key, value := range req.SecretMaterial {
			if req.Application.ExpectedSecretRef(key) == "" || len(value) == 0 || len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
				return Result{}, errors.New("secret input must contain only declared own-application variables with bounded single-line values")
			}
		}
		raw, _ := json.Marshal(req.SecretMaterial)
		commitment, e := localevidence.OwnApplicationSecretCommitment(workspace, req.Name, raw)
		clear(raw)
		if e != nil {
			return Result{}, errors.New("secret review requires the existing private installation authority; offline render accepts references only")
		}
		req.SecretCommitment = commitment
	} else {
		req.SecretCommitment = ""
	}
	if (req.ImageTag != "" || req.AllowMajor) && req.Action != "update" {
		return Result{}, errors.New("image-tag and allow-major belong to a tag update")
	}
	if req.Checkpoint != "" && req.Action != "rollback" {
		return Result{}, errors.New("checkpoint belongs to a rollback that restores data")
	}
	if req.DeleteData && req.Action != "remove" {
		return Result{}, errors.New("delete-data is only admitted for remove")
	}
	if req.ResolvedImage != "" && (req.DryRun || req.Action != "create" && req.Action != "update") {
		return Result{}, errors.New("resolved-image is only admitted for executing create/update")
	}
	if len(req.ResolvedImages) != 0 && (req.DryRun || req.Action != "create" && req.Action != "update") {
		return Result{}, errors.New("resolved-images is only admitted for executing create/update")
	}
	if len(req.ResolvedImages) != 0 && req.Application != nil && !req.Application.IsProject() {
		return Result{}, errors.New("resolved-images belongs to a project application only")
	}
	var result Result
	run := func() error { var err error; result, err = execute(ctx, workspace, installation, req); return err }
	if Mutating(req.Action) && !req.DryRun {
		if !req.OwnerApproved || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`).MatchString(req.OperationID) {
			return result, errors.New("own application mutation requires --owner-approve and a stable --operation-id")
		}
		if req.ReviewDigest == "" {
			return result, errors.New("render the exact action with --dry-run, then pass its --review-digest")
		}
		err := lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, run)
		return result, err
	}
	err := run()
	return result, err
}
func execute(ctx context.Context, workspace string, i contract.Installation, req Request) (Result, error) {
	out := Result{NetworkNotice: "this application cannot make outbound connections", APIVersion: "stackkit.own-application-result/v1", Action: req.Action, DryRun: req.DryRun, BackupVolumes: []string{}}
	s, err := Load(workspace)
	if err != nil {
		return out, err
	}
	current, exists := s.Applications[req.Name]
	input := req
	input.OperationID = ""
	input.ReviewDigest = ""
	input.DryRun = false
	input.OwnerApproved = false
	input.ResolvedImage = ""
	input.ResolvedImages = nil
	requestDigest := hash(input)
	if exists && Mutating(req.Action) && !req.DryRun && current.Operation.ID == req.OperationID {
		if current.Operation.RequestDigest != requestDigest || current.Operation.ReviewDigest != req.ReviewDigest {
			return out, errors.New("operation ID already belongs to a different request")
		}
		if current.Operation.Status == "succeeded" {
			out.Record = current
			out.Outcome = executedOutcome(req.Action)
			if current.Bundle.Application.Firewalled() {
				out.NetworkNotice = firewallNotice(current.Bundle.Application)
				if current.Operation.Firewall != nil {
					out.FirewallPolicyDigest = current.Operation.Firewall.PolicyDigest
				}
			}
			return out, nil
		}
		return out, errors.New("operation outcome is uncertain; inspect status before a new reviewed operation")
	}
	b := current.Bundle
	if req.Application != nil {
		if req.Action != "create" && req.Action != "update" && req.Action != "render" {
			return out, errors.New("document is only admitted for create, update or render")
		}
		if req.Application.Name != req.Name {
			return out, errors.New("name differs from desired document")
		}
		b = contract.Bundle{APIVersion: contract.BundleVersion, Application: *req.Application, Installation: i}
	}
	if req.Action == "create" {
		if req.Application == nil {
			return out, errors.New("create requires a desired-state document")
		}
		if exists && current.State != "removed" && !(current.Operation.Status == "pending" && reflect.DeepEqual(current.Bundle.Application, b.Application)) {
			return out, errors.New("application exists; use update")
		}
		if exists && current.Bundle.Application.Volume != nil && !reflect.DeepEqual(current.Bundle.Application.Volume, b.Application.Volume) {
			return out, errors.New("retained data must keep its original mount path")
		}
		if exists && current.Bundle.Application.IsProject() && ownApplicationVolumesDiffer(current.Bundle.Application, b.Application) {
			return out, errors.New("retained data must keep its original mount path")
		}
		if !exists && len(s.Applications) >= 32 {
			return out, errors.New("at most 32 own application records per installation")
		}
	} else if req.Action != "render" && (!exists || (current.State == "removed" && req.Action != "status" && req.Action != "remove")) {
		return out, errors.New("own application is absent")
	}
	routingChanged := exists && !reflect.DeepEqual(current.Bundle.Installation, i)
	if exists && (current.Bundle.Installation.SiteRef != i.SiteRef || current.Bundle.Installation.NodeRef != i.NodeRef) {
		return out, errors.New("own application belongs to a different installation node")
	}
	if routingChanged && (req.Action == "rollback" || req.Action == "start" || req.Action == "restart") {
		return out, errors.New("routing authority changed; first review an update document using the current hostname")
	}
	// Start and restart run the exact signed bundle again: its admitted local
	// image, network grant and, for a firewalled application, freshly observed
	// host rules through the firewall watcher. They never change intent beyond
	// the power state and never settle an uncertain operation.
	if req.Action == "start" || req.Action == "restart" {
		if current.Operation.Status != "succeeded" || current.TransitionFrom != nil {
			return out, errors.New("an own application operation is pending; inspect status and reconcile it with a reviewed update, rollback, stop or remove")
		}
		if req.Action == "start" && current.State != "stopped" {
			return out, errors.New("start needs a stopped own application; restart a running one")
		}
		if req.Action == "restart" && current.State != "running" {
			return out, errors.New("restart needs a running own application; start a stopped one")
		}
	}

	if req.Action == "update" {
		if req.Application == nil {
			return out, errors.New("update requires the next desired-state document")
		}
		prior, next := current.Bundle.Application, b.Application
		if next.IsProject() || prior.IsProject() {
			if next.APIVersion != prior.APIVersion {
				return out, errors.New("update does not change between the one-container and the project form")
			}
			// A project changes only the image of the services it declares.
			prior.Services = append([]contract.Service(nil), prior.Services...)
			images := ownApplicationServiceImages(next)
			for index := range prior.Services {
				prior.Services[index].Image = images[prior.Services[index].Name]
			}
		} else {
			prior.Image = next.Image
		}
		// v2 adds only the outbound field. No other authority becomes mutable.
		prior.APIVersion = next.APIVersion
		prior.OutboundInternet = next.OutboundInternet
		if prior.IsAdopted() && next.IsAdopted() && len(next.PublishedPorts) == 0 {
			// An adopted application may give up its published host ports (for
			// example to turn outbound off); it never adds or changes one.
			prior.PublishedPorts = nil
		}
		if routingChanged {
			prior.Hostname = next.Hostname
		}
		if !reflect.DeepEqual(prior, next) {
			return out, errors.New("update changes only image, document version and outboundInternet; other application fields, the service set and the service order are immutable")
		}
		if !b.Application.Firewalled() && current.Operation.Status != "pending" && current.State != "stopped" && !routingChanged && ownApplicationSameImages(current.Bundle.Application, b.Application) && reflect.DeepEqual(current.Bundle.Application.OutboundInternet, b.Application.OutboundInternet) && current.Bundle.Application.APIVersion == b.Application.APIVersion && len(current.Bundle.Application.PublishedPorts) == len(b.Application.PublishedPorts) && len(req.SecretMaterial) == 0 {
			return out, errors.New("update requires a changed image, version, outboundInternet, published ports or secret rotation")
		}
	}
	if req.Action == "rollback" {
		if b.Application.IsProject() {
			if len(current.PreviousResolvedImages) == 0 {
				return out, errors.New("no previous successful image reference")
			}
			previous := current.PreviousImages
			services := append([]contract.Service(nil), b.Application.Services...)
			for index := range services {
				image, ok := previous[services[index].Name]
				if !ok {
					return out, errors.New("no previous successful image reference")
				}
				services[index].Image = image
			}
			b.Application.Services = services
			b.ResolvedImages = maps.Clone(current.PreviousResolvedImages)
			b.ResolvedImage = ""
			b.ImageID = ""
			b.ImageIDs = nil
		} else {
			if current.PreviousResolvedImage == "" {
				return out, errors.New("no previous successful image reference")
			}
			b.Application.Image = current.PreviousImage
			// Rollback always revokes the additive v2 grant; re-enabling needs a new
			// reviewed v2 update, never an old image or old kit implicitly granting it.
			// An adopted application keeps the grant and its published ports: the
			// adoption review granted them to the application the original already
			// was, not to one image of it.
			if !b.Application.IsAdopted() {
				b.Application.APIVersion = contract.APIVersion
				b.Application.OutboundInternet = nil
			}
			b.ResolvedImage = current.PreviousResolvedImage
			b.ImageID = ""
		}
	}
	if err = b.Validate(); err != nil {
		return out, err
	}
	if req.Action == "remove" && req.DeleteData && checkpointBlocks(current.Checkpoint) {
		// A kept checkpoint is the only intact data; an unfinished one belongs
		// to a running update. Neither is deleted as a side effect of removal.
		return out, errors.New("the data checkpoint of " + current.Checkpoint.OperationID + " is " + current.Checkpoint.State + "; restore it (--action rollback --checkpoint) or discard it (--action discard-checkpoint) before removing the data")
	}
	if req.KeepSecrets && !b.Application.HasVolume() {
		return out, errors.New("keep-secrets requires a retained named volume")
	}
	for key := range b.Application.SecretSlots() {
		if (req.Action == "create" || req.Action == "update") && req.SecretMaterial[key] == "" && (!exists || current.State == "removed" && !current.SecretsKept) {
			return out, errors.New("create requires private secret input for each declared own-application secret")
		}
	}
	if req.Action == "create" || req.Action == "update" || req.Action == "render" {
		if b.Application.IsProject() {
			if err = resolveProjectImages(ctx, &b, req); err != nil {
				return out, err
			}
		} else if req.Action == "render" {
			_, d, _ := contract.SplitImage(b.Application.Image)
			if d == "" {
				return out, errors.New("offline render requires a digest-pinned image; use create/update dry review to resolve a tag")
			}
			b.ResolvedImage = b.Application.Image
		} else if req.DryRun {
			if req.ResolvedImage != "" {
				return out, errors.New("dry review resolves its own registry digest; do not supply resolved-image")
			}
			b.ResolvedImage, err = nativehost.ResolveOwnApplicationImage(ctx, b.Application.Image)
		} else {
			_, d, _ := contract.SplitImage(b.Application.Image)
			if d != "" {
				if req.ResolvedImage != "" && req.ResolvedImage != b.Application.Image {
					return out, errors.New("resolved-image differs from desired digest")
				}
				b.ResolvedImage = b.Application.Image
			} else {
				b.ResolvedImage = req.ResolvedImage
			}
			if b.ResolvedImage == "" {
				return out, errors.New("tagged execution requires the resolved-image from the reviewed registry manifest")
			}
		}
		if err != nil {
			return out, err
		}
		if err = b.Validate(); err != nil {
			return out, err
		}
	}
	policyDigest, rulesetDigest := "", ""
	if b.Application.Firewalled() {
		if current.Operation.Firewall != nil {
			rulesetDigest = current.Operation.Firewall.RulesetDigest
		}
		if req.Action == "create" || req.Action == "update" || req.Action == "render" || req.Action == "start" || req.Action == "restart" {
			guard, e := observeFirewall(ctx, "stackkit-"+b.Application.WorkloadRef()+"-"+i.NodeRef+"_private", b.Application.OutboundEnabled())
			if e != nil {
				return out, e
			}
			rulesetDigest = guard.Policy.Digest()
		}
		out.FirewallRulesetDigest = rulesetDigest
		policyDigest = ownappfirewall.DefinitionDigest()
		out.FirewallPolicyDigest = policyDigest
		out.NetworkNotice = firewallNotice(b.Application)
	}
	if b.Application.IsProject() {
		out.ReviewDigest = hash(struct {
			FirewallPolicy string
			StaticRuleset  string
			PriorFirewall  *ownappfirewall.Receipt
			Request        any
			Revision       string
			Installation   contract.Installation
			ResolvedImages map[string]string
		}{policyDigest, rulesetDigest, current.Operation.Firewall, input, current.Revision, i, b.ResolvedImages})
	} else {
		out.ReviewDigest = hash(struct {
			FirewallPolicy string
			StaticRuleset  string
			PriorFirewall  *ownappfirewall.Receipt
			Request        any
			Revision       string
			Installation   contract.Installation
			ResolvedImage  string
		}{policyDigest, rulesetDigest, current.Operation.Firewall, input, current.Revision, i, b.ResolvedImage})
	}
	out.Record = current
	out.Record.Bundle = b
	route := architecturev2renderer.OwnApplicationAccessRoute(b)
	out.Route = &route
	if b.Application.IsProject() {
		for _, s := range b.Application.Services {
			if s.Volume != nil {
				out.BackupVolumes = append(out.BackupVolumes, localbackuppolicy.StandaloneComposeVolumeName(b.Application.WorkloadRef(), i.NodeRef, s.Name, "data"))
			}
		}
	} else if b.Application.Volume != nil {
		out.BackupVolumes = []string{localbackuppolicy.StandaloneComposeVolumeName(b.Application.WorkloadRef(), i.NodeRef, "app", "data")}
	}
	if req.DryRun || req.Action == "render" {
		compose, err := nativehost.RenderOwnApplication(b)
		out.Compose = string(compose)
		out.Outcome = "reviewed"
		return out, err
	}
	runtime, err := nativehost.NewOwnApplicationRuntime(workspace)
	if err != nil {
		return out, err
	}
	runtime.ExpectFirewallRuleset(rulesetDigest)
	if !Mutating(req.Action) {
		out.Observation, err = runtime.Read(ctx, b, req.Action == "logs")
		if err != nil && req.Action == "status" {
			out.ObservationError = "runtime observation unavailable; signed desired state is not evidence of runtime health"
			out.Outcome = "observation_unavailable"
			return out, nil
		}
		out.Outcome = "observed"
		return out, err
	}
	if req.ReviewDigest != out.ReviewDigest {
		return out, errors.New("review digest is stale or differs from the exact action, document, installation or current revision")
	}
	if req.Action == "create" || req.Action == "update" || req.Action == "rollback" {
		if b, err = runtime.AdmitImage(ctx, b); err != nil {
			return out, err
		}
	}
	next := current
	next.Bundle = b
	// Keep the exact old recipe in signed write-ahead custody across interrupted
	// network replacement, so a new reconciliation can finish exact teardown.
	if exists && current.TransitionFrom == nil && (req.Action == "create" || req.Action == "update" || req.Action == "rollback") && (current.Bundle.Application.Firewalled() || current.Bundle.Application.Firewalled() != b.Application.Firewalled()) {
		prior := current.Bundle
		next.TransitionFrom = &prior
	}
	if req.Action == "create" {
		next.Generation = hash(struct{ Prior, ID string }{current.Generation, req.OperationID})
		// A create starts a new generation: the previous generation's image
		// custody belongs to the removed record and never carries into it.
		clearPreviousImageCustody(&next)
	}
	next.Revision = hash(b)
	if (req.Action == "update" || req.Action == "rollback") && current.Operation.Status != "pending" {
		if b.Application.IsProject() {
			if !ownApplicationSameImages(current.Bundle.Application, b.Application) {
				next.PreviousImages = ownApplicationServiceImages(current.Bundle.Application)
				next.PreviousResolvedImages = maps.Clone(current.Bundle.ResolvedImages)
			}
		} else if current.Bundle.Application.Image != b.Application.Image {
			next.PreviousImage = current.Bundle.Application.Image
			next.PreviousResolvedImage = current.Bundle.ResolvedImage
		}
	}
	next.Operation = Operation{ID: req.OperationID, Action: req.Action, RequestDigest: requestDigest, ReviewDigest: req.ReviewDigest, Status: "pending", At: time.Now().UTC()}
	next.Operation.Firewall = current.Operation.Firewall
	// Write-ahead custody prevents a crashed or failed call being replayed as
	// an unobserved success. Status retains the exact pending image and action.
	s.Applications[req.Name] = next
	if err = save(workspace, s); err != nil {
		return out, err
	}
	switch req.Action {
	case "create", "update", "rollback":
		old := current.Bundle
		if next.TransitionFrom != nil {
			old = *next.TransitionFrom
		}
		if exists && (old.Application.Firewalled() || old.Application.Firewalled() != b.Application.Firewalled()) {
			if current.Operation.Status == "pending" {
				// A reviewed reconciliation may rebuild the signed scrubbed recipe;
				// a crashed prior prepare need not have left a complete runtime file.
				if err = runtime.ReconcilePendingTeardown(ctx, old); err != nil {
					return out, err
				}
			}
			err = runtime.Control(ctx, old, "remove", false, current.Generation)
			if err == nil {
				next.TransitionFrom = nil
				s.Applications[req.Name] = next
				err = save(workspace, s)
			}
		}
		if err == nil {
			// Old teardown must still be able to verify the old custody-backed recipe.
			// Rotate secrets only after its exact container/network has been removed.
			for key, value := range req.SecretMaterial {
				if err = localevidence.StoreEncryptedLocalIssuedSecret(workspace, b.Application.ExpectedSecretRef(key), []byte(value)); err != nil {
					return out, errors.New("own application secret custody write pending")
				}
			}

			err = runtime.Apply(ctx, b)
		}
		if err == nil {
			if receipt := runtime.FirewallReceipt(); receipt != nil {
				next.Operation.Firewall = receipt
				next.Operation.FirewallAction = "applied"
			} else if current.Bundle.Application.Firewalled() {
				next.Operation.FirewallAction = "removed"
			}
		}
	case "start", "restart":
		if req.Action == "restart" {
			err = runtime.Control(ctx, b, "stop", false, current.Generation)
		}
		if err == nil {
			err = runtime.Apply(ctx, b)
		}
		if err == nil {
			if receipt := runtime.FirewallReceipt(); receipt != nil {
				next.Operation.Firewall = receipt
				next.Operation.FirewallAction = "applied"
			}
		}
	case "stop", "remove":
		controlled := b
		if next.TransitionFrom != nil {
			controlled = *next.TransitionFrom
		}
		err = runtime.Control(ctx, controlled, req.Action, req.DeleteData, current.Generation)
	}
	if err != nil {
		return out, fmt.Errorf("%w; inspect status before a new reviewed operation: %w", ErrPending, err)
	}
	switch req.Action {
	case "create":
		next.State = "running"
	case "update", "rollback", "start", "restart":
		next.State = "running"
	case "stop":
		next = stoppedRecord(next)
	case "remove":
		if b.Application.Firewalled() {
			next.Operation.FirewallAction = "removed"
		}
		next.SecretsKept = req.KeepSecrets
		if !req.KeepSecrets {
			for _, ref := range b.Application.SecretRefList() {
				if err = localevidence.RemoveLocalIssuedSecret(workspace, ref); err != nil {
					return out, errors.New("own application secret removal pending")
				}
			}
		}
		next.State = "removed"
		if req.DeleteData && next.Checkpoint != nil {
			// Deleting the data deletes its retained checkpoint copy as well.
			if err = dropCheckpoint(ctx, workspace, next.Bundle, *next.Checkpoint); err != nil {
				return out, errors.New("own application checkpoint removal pending: " + err.Error())
			}
			next.Checkpoint = nil
		}
		if req.DeleteData {
			if next.Bundle.Application.IsProject() {
				services := append([]contract.Service(nil), next.Bundle.Application.Services...)
				for index := range services {
					services[index].Volume = nil
				}
				next.Bundle.Application.Services = services
			} else {
				next.Bundle.Application.Volume = nil
			}
			next.Revision = hash(next.Bundle)
		}
	}
	next.TransitionFrom = nil
	next.Operation.Status = "succeeded"
	next.Operation.At = time.Now().UTC()
	s.Applications[req.Name] = next
	if err = save(workspace, s); err != nil {
		return out, err
	}
	out.Record = next
	out.Outcome = executedOutcome(req.Action)
	return out, nil
}

// AccessTable extends only the server's already admitted table. Invalid or
// unsigned custody fails closed; names cannot replace an existing route.
func AccessTable(workspace string, base accessgatetable.Table) (accessgatetable.Table, error) {
	s, err := loadSigned(workspace)
	if err != nil {
		return base, err
	}
	base.Routes = append([]accessgatetable.Route(nil), base.Routes...)
	names := make([]string, 0, len(s.Applications))
	for name := range s.Applications {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := s.Applications[name]
		if validateRecord(name, r) != nil {
			continue
		}
		if r.State == "removed" || r.State == "stopped" || r.Operation.Status != "succeeded" {
			continue
		}
		route := architecturev2renderer.OwnApplicationAccessRoute(r.Bundle)
		collision := false
		for _, existing := range base.Routes {
			if existing.Host == route.Host || existing.ID == route.ID {
				collision = true
				break
			}
		}
		if collision {
			continue
		}
		base.Routes = append(base.Routes, route)
	}
	return base, base.Validate()
}
func ExtendBackupPolicy(workspace string, p localbackuppolicy.Policy) (localbackuppolicy.Policy, error) {
	s, err := Load(workspace)
	if err != nil {
		return p, err
	}
	source := p.SourceProjection()
	changed := false
	for _, r := range s.Applications {
		if !r.Bundle.Application.HasVolume() {
			continue
		}
		if err = RequireBackupCoverage(p); err != nil {
			return p, err
		}
		if r.Operation.Status != "succeeded" {
			return p, errors.New("own application has an uncertain operation; backup cannot infer its data custody")
		}
		if r.Bundle.Installation.SiteRef != p.Target.SiteRef || r.Bundle.Installation.NodeRef != p.Target.NodeRef {
			return p, errors.New("own application backup targets a different node")
		}
		volumes, runtimes, err := architecturev2renderer.OwnApplicationBackup(r.Bundle)
		if err != nil {
			return p, err
		}
		if r.Bundle.Application.Firewalled() {
			if r.Operation.Firewall == nil {
				return p, errors.New("own application backup has no signed firewall receipt")
			}
			for n := range runtimes {
				runtimes[n].FirewallRulesetDigest = r.Operation.Firewall.RulesetDigest
			}
		}
		source.ApplicationVolumes = append(source.ApplicationVolumes, volumes...)
		if r.State == "removed" {
			for n := range runtimes {
				runtimes[n].Retained = true
			}
		}
		source.ApplicationRuntimes = append(source.ApplicationRuntimes, runtimes...)
		changed = true
	}
	if !changed {
		return p, nil
	}
	var extended localbackuppolicy.Source
	if source.CoreModuleRef == "" {
		extended, err = localbackuppolicy.GovernedSourceWithApplicationVolumesAndRuntimes(source.ApplicationVolumes, source.ApplicationRuntimes)
	} else {
		extended, err = localbackuppolicy.GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule(source.CoreModuleRef, source.ApplicationVolumes, source.ApplicationRuntimes)
	}
	if err != nil {
		return p, err
	}
	extended, err = extended.WithMarketplaceServices(source.MarketplaceServices)
	if err != nil {
		return p, err
	}
	p.Source = extended
	return p, nil
}

// RequireBackupCoverage preserves the installation's existing coverage grant;
// own application data never silently widens a paid or owner-selected policy.
func RequireBackupCoverage(p localbackuppolicy.Policy) error {
	if len(p.DataClasses) > 0 && !slices.Contains(p.DataClasses, "user-content") {
		return errors.New("an own application volume requires existing user-content backup coverage")
	}
	return nil
}

var observeFirewall = ownappfirewall.New

// resolveProjectImages resolves and pins the image of every service of a
// project. An offline render requires digest-pinned images; a dry review
// resolves every tag from the public registry; an execution takes the exact
// digests that review returned.
func resolveProjectImages(ctx context.Context, b *contract.Bundle, req Request) error {
	for name := range req.ResolvedImages {
		if _, ok := b.Application.Service(name); !ok {
			return errors.New("resolved-images names a service the application does not declare")
		}
	}
	resolved := map[string]string{}
	for _, s := range b.Application.Services {
		_, digest, _ := contract.SplitImage(s.Image)
		switch {
		case req.Action == "render":
			if digest == "" {
				return errors.New("offline render requires a digest-pinned image for every service; use create/update dry review to resolve a tag")
			}
			resolved[s.Name] = s.Image
		case req.DryRun:
			if digest != "" {
				resolved[s.Name] = s.Image
				continue
			}
			pinned, err := nativehost.ResolveOwnApplicationImage(ctx, s.Image)
			if err != nil {
				return err
			}
			resolved[s.Name] = pinned
		case digest != "":
			if supplied := req.ResolvedImages[s.Name]; supplied != "" && supplied != s.Image {
				return errors.New("resolved-images differs from desired digest")
			}
			resolved[s.Name] = s.Image
		default:
			supplied := req.ResolvedImages[s.Name]
			if supplied == "" {
				return errors.New("tagged execution requires the resolved-images from the reviewed registry manifest")
			}
			resolved[s.Name] = supplied
		}
	}
	b.ResolvedImages = resolved
	return nil
}

// clearPreviousImageCustody drops the previous generation's image custody from
// a record. A create starts a new generation, and the removed record's custody
// belongs to the old form: the one-container custody names an image and a port
// the project form does not have, and the project custody names services the
// new document may not declare.
func clearPreviousImageCustody(r *Record) {
	r.PreviousImage = ""
	r.PreviousResolvedImage = ""
	r.PreviousImages = nil
	r.PreviousResolvedImages = nil
}

// ownApplicationServiceImages maps every declared project service to its
// desired image.
func ownApplicationServiceImages(a contract.Application) map[string]string {
	images := map[string]string{}
	for _, s := range a.Services {
		images[s.Name] = s.Image
	}
	return images
}

// ownApplicationSameImages reports whether two applications declare the same
// images: one container, or every service of a project.
func ownApplicationSameImages(prior, next contract.Application) bool {
	if prior.IsProject() && next.IsProject() {
		return reflect.DeepEqual(ownApplicationServiceImages(prior), ownApplicationServiceImages(next))
	}
	return prior.Image == next.Image
}

// ownApplicationVolumesDiffer reports whether the declared named volumes of a
// project differ, so retained data never changes its mount path.
func ownApplicationVolumesDiffer(prior, next contract.Application) bool {
	volumes := func(a contract.Application) map[string]string {
		declared := map[string]string{}
		for _, s := range a.Services {
			if s.Volume != nil {
				declared[s.Name] = s.Volume.MountPath
			}
		}
		return declared
	}
	return !reflect.DeepEqual(volumes(prior), volumes(next))
}

// firewallNotice states what the managed host firewall grants a firewalled
// application: public IPv4 Internet, or with published ports only, nothing
// but replies to connections made to it.
func firewallNotice(a contract.Application) string {
	const reboot = "IPv6 disabled; after a host reboot it starts again only once the reviewed rules are restored and verified"
	if !a.OutboundEnabled() {
		return "no outbound Internet; published ports and its hostname only answer connections made to it, through verified host firewall; " + reboot
	}
	return "public IPv4 Internet only through verified host firewall; no home, host or special-use destinations; " + reboot
}

func stoppedRecord(r Record) Record {
	if r.Bundle.Application.Firewalled() {
		r.Operation.FirewallAction = "retained"
	}
	r.State = "stopped"
	return r
}
