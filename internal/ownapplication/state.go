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
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const statePath = ".stackkit/own-applications/state.json"
const stateVersion = "stackkit.own-application-custody/v1"

type Record struct {
	SecretsKept           bool            `json:"secretsKept,omitempty"`
	Generation            string          `json:"generation"`
	Bundle                contract.Bundle `json:"bundle"`
	PreviousImage         string          `json:"previousImage,omitempty"`
	PreviousResolvedImage string          `json:"previousResolvedImage,omitempty"`
	State                 string          `json:"state"`
	Revision              string          `json:"revision"`
	Operation             Operation       `json:"operation"`
}
type Operation struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	RequestDigest string    `json:"requestDigest"`
	ReviewDigest  string    `json:"reviewDigest"`
	Status        string    `json:"status"`
	At            time.Time `json:"at"`
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
	SecretMaterial   map[string]string     `json:"-"`
	SecretCommitment string                `json:"secretCommitment,omitempty"`
	DeleteData       bool                  `json:"deleteData,omitempty"`
	OperationID      string                `json:"operationId,omitempty"`
	ReviewDigest     string                `json:"reviewDigest,omitempty"`
	DryRun           bool                  `json:"dryRun,omitempty"`
	OwnerApproved    bool                  `json:"-"`
}
type Result struct {
	APIVersion       string                 `json:"apiVersion"`
	NetworkNotice    string                 `json:"networkNotice"`
	Action           string                 `json:"action"`
	DryRun           bool                   `json:"dryRun"`
	ReviewDigest     string                 `json:"reviewDigest,omitempty"`
	Record           Record                 `json:"record"`
	Compose          string                 `json:"compose,omitempty"`
	Route            *accessgatetable.Route `json:"route,omitempty"`
	BackupVolumes    []string               `json:"backupVolumes"`
	Observation      json.RawMessage        `json:"observation,omitempty"`
	ObservationError string                 `json:"observationError,omitempty"`
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
	if r.Operation.Status != "pending" && r.Operation.Status != "succeeded" {
		return errors.New("invalid own application operation state")
	}
	switch r.State {
	case "", "running", "stopped", "removed":
	default:
		return errors.New("invalid own application state")
	}
	if r.PreviousResolvedImage != "" {
		prior := r.Bundle
		prior.Application.Image = r.PreviousImage
		prior.ResolvedImage = r.PreviousResolvedImage
		if prior.Validate() != nil {
			return errors.New("invalid previous image custody")
		}
	}
	if name != r.Bundle.Application.Name || r.Bundle.Validate() != nil || r.Bundle.ResolvedImage == "" || r.Revision != hash(r.Bundle) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(r.Generation) {
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
	return action == "create" || action == "update" || action == "rollback" || action == "stop" || action == "remove"
}
func Execute(ctx context.Context, workspace string, installation contract.Installation, req Request) (Result, error) {
	if !contract.ValidName(req.Name) {
		return Result{}, errors.New("valid own application name is required")
	}
	switch req.Action {
	case "create", "update", "rollback", "stop", "remove", "status", "logs", "render":
	default:
		return Result{}, errors.New("action must be create, update, rollback, stop, remove, status, logs or render")
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
			if req.Application.SecretRefs[key] != "secret://"+req.Application.WorkloadRef()+"/"+key || len(value) == 0 || len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
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
	if req.DeleteData && req.Action != "remove" {
		return Result{}, errors.New("delete-data is only admitted for remove")
	}
	if req.ResolvedImage != "" && (req.DryRun || req.Action != "create" && req.Action != "update") {
		return Result{}, errors.New("resolved-image is only admitted for executing create/update")
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
	requestDigest := hash(input)
	if exists && Mutating(req.Action) && !req.DryRun && current.Operation.ID == req.OperationID {
		if current.Operation.RequestDigest != requestDigest || current.Operation.ReviewDigest != req.ReviewDigest {
			return out, errors.New("operation ID already belongs to a different request")
		}
		if current.Operation.Status == "succeeded" {
			out.Record = current
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
	if routingChanged && req.Action == "rollback" {
		return out, errors.New("routing authority changed; first review an update document using the current hostname")
	}

	if req.Action == "update" {
		if req.Application == nil {
			return out, errors.New("update requires the next desired-state document")
		}
		prior, next := current.Bundle.Application, b.Application
		prior.Image = next.Image
		if routingChanged {
			prior.Hostname = next.Hostname
		}
		if !reflect.DeepEqual(prior, next) {
			return out, errors.New("v1 update changes only the image reference; other application fields are immutable")
		}
		if current.Operation.Status != "pending" && current.State != "stopped" && !routingChanged && current.Bundle.Application.Image == b.Application.Image && len(req.SecretMaterial) == 0 {
			return out, errors.New("update requires a different image reference")
		}
	}
	if req.Action == "rollback" {
		if current.PreviousResolvedImage == "" {
			return out, errors.New("no previous successful image reference")
		}
		b.Application.Image = current.PreviousImage
		b.ResolvedImage = current.PreviousResolvedImage
	}
	if err = b.Validate(); err != nil {
		return out, err
	}
	if req.KeepSecrets && b.Application.Volume == nil {
		return out, errors.New("keep-secrets requires a retained named volume")
	}
	for key := range b.Application.SecretRefs {
		if (req.Action == "create" || req.Action == "update") && req.SecretMaterial[key] == "" && (!exists || current.State == "removed" && !current.SecretsKept) {
			return out, errors.New("create requires private secret input for each declared own-application secret")
		}
	}
	if req.Action == "create" || req.Action == "update" || req.Action == "render" {
		if req.Action == "render" {
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
	out.ReviewDigest = hash(struct {
		Request       any
		Revision      string
		Installation  contract.Installation
		ResolvedImage string
	}{input, current.Revision, i, b.ResolvedImage})
	out.Record = current
	out.Record.Bundle = b
	route := architecturev2renderer.OwnApplicationAccessRoute(b)
	out.Route = &route
	if b.Application.Volume != nil {
		out.BackupVolumes = []string{localbackuppolicy.StandaloneComposeVolumeName(b.Application.WorkloadRef(), i.NodeRef, "app", "data")}
	}
	if req.DryRun || req.Action == "render" {
		compose, err := nativehost.RenderOwnApplication(b)
		out.Compose = string(compose)
		return out, err
	}
	runtime, err := nativehost.NewOwnApplicationRuntime(workspace)
	if err != nil {
		return out, err
	}
	if !Mutating(req.Action) {
		out.Observation, err = runtime.Read(ctx, b, req.Action == "logs")
		if err != nil && req.Action == "status" {
			out.ObservationError = "runtime observation unavailable; signed desired state is not evidence of runtime health"
			return out, nil
		}
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
	if req.Action == "create" {
		next.Generation = hash(struct{ Prior, ID string }{current.Generation, req.OperationID})
	}
	next.Revision = hash(b)
	if (req.Action == "update" || req.Action == "rollback") && current.Operation.Status != "pending" && current.Bundle.Application.Image != b.Application.Image {
		next.PreviousImage = current.Bundle.Application.Image
		next.PreviousResolvedImage = current.Bundle.ResolvedImage
	}
	next.Operation = Operation{ID: req.OperationID, Action: req.Action, RequestDigest: requestDigest, ReviewDigest: req.ReviewDigest, Status: "pending", At: time.Now().UTC()}
	// Write-ahead custody prevents a crashed or failed call being replayed as
	// an unobserved success. Status retains the exact pending image and action.
	s.Applications[req.Name] = next
	if err = save(workspace, s); err != nil {
		return out, err
	}
	for key, value := range req.SecretMaterial {
		if err = localevidence.StoreEncryptedLocalIssuedSecret(workspace, b.Application.SecretRefs[key], []byte(value)); err != nil {
			return out, errors.New("own application secret custody write pending")
		}
	}
	switch req.Action {
	case "create", "update", "rollback":
		err = runtime.Apply(ctx, b)
	case "stop", "remove":
		err = runtime.Control(ctx, b, req.Action, req.DeleteData, current.Generation)
	}
	if err != nil {
		return out, fmt.Errorf("own application operation pending; inspect status before a new reviewed operation: %w", err)
	}
	switch req.Action {
	case "create":
		next.State = "running"
	case "update", "rollback":
		next.State = "running"
	case "stop":
		next.State = "stopped"
	case "remove":
		next.SecretsKept = req.KeepSecrets
		if !req.KeepSecrets {
			for _, ref := range b.Application.SecretRefs {
				if err = localevidence.RemoveLocalIssuedSecret(workspace, ref); err != nil {
					return out, errors.New("own application secret removal pending")
				}
			}
		}
		next.State = "removed"
		if req.DeleteData {
			next.Bundle.Application.Volume = nil
			next.Revision = hash(next.Bundle)
		}
	}
	next.Operation.Status = "succeeded"
	next.Operation.At = time.Now().UTC()
	s.Applications[req.Name] = next
	if err = save(workspace, s); err != nil {
		return out, err
	}
	out.Record = next
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
		if r.Bundle.Application.Volume == nil {
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
