package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
)

const basementCoreProcessOutputLimit = 256 << 10

type basementCoreProcessRunner interface {
	Run(context.Context, []string, string, []string) ([]byte, error)
}

type basementCoreProber interface {
	Probe(context.Context, BasementCoreHealthExpectation) error
}

type BasementCoreObservationDriftError struct {
	subject, code, projectRef string
	cause                     error
}

func (err *BasementCoreObservationDriftError) Error() string {
	return "verified Basement runtime differs from the authorized project"
}

func (err *BasementCoreObservationDriftError) Unwrap() error { return err.cause }
func (err *BasementCoreObservationDriftError) DriftSubject() string {
	return err.subject
}
func (err *BasementCoreObservationDriftError) DriftCode() string { return err.code }
func (err *BasementCoreObservationDriftError) DriftProjectRef() string {
	return err.projectRef
}

func basementCoreObservationDrift(projectRef, code string, cause error) error {
	return &BasementCoreObservationDriftError{
		subject: "runtime-configuration", code: code, projectRef: projectRef, cause: cause,
	}
}

type osBasementCoreOperations struct {
	workspaceRoot string
	runner        basementCoreProcessRunner
	prober        basementCoreProber
	ownerIdentity basementOwnerIdentity
}

type basementOwnerIdentity interface {
	Realize(context.Context) (localevidence.OwnerRuntimeBinding, error)
	Verify(context.Context) (localevidence.OwnerRuntimeBinding, error)
}

type localBasementOwnerIdentity struct {
	service *localowner.Service
}

// NewOSBasementCoreOperations explicitly grants the local workspace's fixed
// Docker Compose capability. The workspace is construction-owned and all
// request-controlled paths, executables, endpoints, and credentials are
// excluded from the Operations boundary.
func NewOSBasementCoreOperations(workspaceRoot string) (BasementCoreOperations, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("Basement core operations require an absolute workspace root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Basement core operations require an existing plain workspace directory")
	}
	ownerIdentity, err := localowner.NewService(absolute)
	if err != nil {
		return nil, err
	}
	return &osBasementCoreOperations{
		workspaceRoot: filepath.Clean(absolute),
		runner:        osBasementCoreProcessRunner{},
		prober:        osBasementCoreProber{},
		ownerIdentity: localBasementOwnerIdentity{service: ownerIdentity},
	}, nil
}

func (o *osBasementCoreOperations) ApplyProject(ctx context.Context, project BasementCoreProject) (BasementCoreApplyObservation, error) {
	if err := o.ready(ctx); err != nil {
		return BasementCoreApplyObservation{}, err
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspaceRoot); err != nil {
		return BasementCoreApplyObservation{}, fmt.Errorf("verify local Basement runtime custody before Apply: %w", err)
	}
	composePath, err := o.persistCompose(project)
	if err != nil {
		return BasementCoreApplyObservation{}, err
	}
	if _, err := o.runner.Run(ctx, basementCoreComposeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return BasementCoreApplyObservation{}, errors.New("local Docker Compose Apply did not complete")
	}
	binding, err := o.ownerIdentity.Realize(ctx)
	if err != nil {
		return BasementCoreApplyObservation{}, errors.New("local PocketID owner realization did not complete")
	}
	// Owner realization creates the exact PocketID OIDC client and installs its
	// one-time secret as a private optional env override. Reconcile Compose once
	// more so TinyAuth runs with that bound credential before Apply can succeed.
	if _, err := o.runner.Run(ctx, basementCoreComposeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return BasementCoreApplyObservation{}, errors.New("local TinyAuth PocketID binding did not complete")
	}
	return BasementCoreApplyObservation{
		ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest,
		OwnerRef: binding.OwnerRef, PocketIDSubject: binding.PocketIDSubject,
		OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding), Status: "applied",
	}, nil
}

func (o *osBasementCoreOperations) VerifyProject(ctx context.Context, project BasementCoreProject) (BasementCoreVerifyObservation, error) {
	if err := o.ready(ctx); err != nil {
		return BasementCoreVerifyObservation{}, err
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspaceRoot); err != nil {
		return BasementCoreVerifyObservation{}, fmt.Errorf("verify local Basement runtime custody before observation: %w", err)
	}
	composePath := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "basement-core", "compose.yaml")
	content, err := readStablePrivateBasementRuntimeFile(o.workspaceRoot, composePath)
	if err != nil {
		return BasementCoreVerifyObservation{}, fmt.Errorf("verify Basement core Compose authority: %w", err)
	}
	if !bytes.Equal(content, project.Definition) {
		return BasementCoreVerifyObservation{}, basementCoreObservationDrift(
			project.ProjectRef, "runtime-compose-changed", nil,
		)
	}
	raw, err := o.runner.Run(ctx, basementCoreComposeArgs(composePath, "ps"), filepath.Dir(composePath), o.environment())
	if err != nil {
		if isBasementCoreContextTermination(err) {
			return BasementCoreVerifyObservation{}, err
		}
		return BasementCoreVerifyObservation{}, basementCoreObservationDrift(
			project.ProjectRef, "runtime-status-unavailable", err,
		)
	}
	services, err := parseBasementCoreComposeStatus(raw, project.Services)
	if err != nil {
		return BasementCoreVerifyObservation{}, basementCoreObservationDrift(
			project.ProjectRef, "runtime-service-set-changed", err,
		)
	}
	probes := make([]BasementCoreProbeObservation, 0, len(project.Health))
	for _, expectation := range project.Health {
		// Container health is proven by the exact pinned Compose service
		// observation above; HTTP and TCP contracts require direct probes.
		if expectation.Kind != "contract" && expectation.Kind != "container" {
			if err := o.prober.Probe(ctx, expectation); err != nil {
				if isBasementCoreContextTermination(err) {
					return BasementCoreVerifyObservation{}, err
				}
				return BasementCoreVerifyObservation{}, basementCoreObservationDrift(
					project.ProjectRef, "runtime-health-changed", err,
				)
			}
		}
		probes = append(probes, BasementCoreProbeObservation{
			RequirementID: expectation.RequirementID, Status: "healthy",
		})
	}
	binding, err := o.ownerIdentity.Verify(ctx)
	if err != nil {
		return BasementCoreVerifyObservation{}, errors.New("local PocketID owner verification did not complete")
	}
	return BasementCoreVerifyObservation{
		ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest,
		OwnerRef: binding.OwnerRef, PocketIDSubject: binding.PocketIDSubject,
		OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding),
		Status:             "ready", Services: services, Probes: probes,
	}, nil
}

func readStablePrivateBasementRuntimeFile(workspaceRoot, absolutePath string) ([]byte, error) {
	before, err := os.Lstat(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("inspect Basement runtime file before read: %w", err)
	}
	if err := requirePrivateBasementRuntimeFile(absolutePath, before); err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	relative, err := filepath.Rel(workspaceRoot, absolutePath)
	if err != nil {
		return nil, errors.New("Basement runtime file is outside the workspace")
	}
	content, info, err := transaction.ReadStable(filepath.ToSlash(relative))
	if err != nil {
		return nil, err
	}
	if len(content) > basementCoreProcessOutputLimit {
		return nil, errors.New("Basement runtime file exceeds the bounded size")
	}
	if !os.SameFile(before, info) {
		return nil, errors.New("Basement runtime file changed between authority check and stable read")
	}
	if err := requirePrivateBasementRuntimeFile(absolutePath, info); err != nil {
		return nil, err
	}
	return content, nil
}

func isBasementCoreContextTermination(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (o *osBasementCoreOperations) ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Basement core operations require a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil || o.workspaceRoot == "" || o.runner == nil || o.prober == nil || o.ownerIdentity == nil {
		return errors.New("Basement core operations are not initialized")
	}
	return nil
}

func (o *osBasementCoreOperations) persistCompose(project BasementCoreProject) (string, error) {
	runtimeDir := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "basement-core")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create private Basement runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("restrict private Basement runtime directory: %w", err)
	}
	root, err := confinedfs.Open(runtimeDir)
	if err != nil {
		return "", fmt.Errorf("open private Basement runtime directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return "", fmt.Errorf("bind private Basement runtime directory: %w", err)
	}
	result, err := view.WriteAtomic0600("compose.yaml", project.Definition)
	if err != nil {
		return "", fmt.Errorf("atomically persist Basement core Compose definition: %w", err)
	}
	if !result.Installed || !result.FileSynced {
		return "", errors.New("Basement core Compose persistence did not prove an installed private artifact")
	}
	target := filepath.Join(runtimeDir, "compose.yaml")
	if err := restrictBasementRuntimeFile(target); err != nil {
		return "", fmt.Errorf("restrict Basement core Compose artifact: %w", err)
	}
	return target, nil
}

func (o *osBasementCoreOperations) environment() []string {
	return []string{
		"LANG=C", "LC_ALL=C",
		"STACKKIT_CUSTODY_DIR=" + filepath.Join(o.workspaceRoot, ".stackkit", "custody"),
	}
}

func basementCoreComposeArgs(composePath, operation string) []string {
	prefix := []string{"compose", "--project-name", "stackkit-basement-core", "-f", composePath}
	if operation == "up" {
		return append(prefix, "up", "-d", "--wait", "--wait-timeout", "600")
	}
	return append(prefix, "ps", "--all", "--format", "json")
}

type osBasementCoreProcessRunner struct{}

func (osBasementCoreProcessRunner) Run(ctx context.Context, args []string, directory string, environment []string) ([]byte, error) {
	if len(args) < 6 || args[0] != "compose" || args[1] != "--project-name" ||
		args[2] != "stackkit-basement-core" || args[3] != "-f" ||
		filepath.Clean(args[4]) != args[4] || filepath.Base(args[4]) != "compose.yaml" ||
		filepath.Dir(args[4]) != directory ||
		(!slices.Equal(args[5:], []string{"up", "-d", "--wait", "--wait-timeout", "600"}) &&
			!slices.Equal(args[5:], []string{"ps", "--all", "--format", "json"})) {
		return nil, errors.New("process is outside the closed Basement core Docker Compose contract")
	}
	executable, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("required local Docker runtime is not installed")
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	output := &basementCoreBoundedBuffer{remaining: basementCoreProcessOutputLimit}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, errors.New("bounded local Docker Compose process failed")
	}
	if output.exceeded {
		return nil, errors.New("local Docker Compose process output exceeded the bound")
	}
	return append([]byte(nil), output.Bytes()...), nil
}

type basementCoreBoundedBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (b *basementCoreBoundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
		b.exceeded = true
	}
	b.remaining -= len(value)
	_, _ = b.Buffer.Write(value)
	return original, nil
}

type basementCoreComposePS struct {
	Service string `json:"Service"`
	Image   string `json:"Image"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

func parseBasementCoreComposeStatus(raw []byte, expected []BasementCoreServiceExpectation) ([]BasementCoreServiceObservation, error) {
	var rows []basementCoreComposePS
	if err := json.Unmarshal(raw, &rows); err != nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var row basementCoreComposePS
			if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, errors.New("Docker Compose status is not bounded JSON")
			}
			rows = append(rows, row)
		}
	}
	if len(rows) != len(expected) {
		return nil, errors.New("Docker Compose status does not contain the exact Basement core service set")
	}
	expectedByRef := make(map[string]BasementCoreServiceExpectation, len(expected))
	for _, service := range expected {
		expectedByRef[service.Ref] = service
	}
	result := make([]BasementCoreServiceObservation, 0, len(rows))
	for _, row := range rows {
		want, ok := expectedByRef[row.Service]
		wantImage := want.ImageRef + "@" + want.ImageDigest
		health := strings.ToLower(strings.TrimSpace(row.Health))
		if !ok || row.Image != wantImage || strings.ToLower(row.State) != "running" ||
			(want.HealthRequired && health != "healthy") ||
			(!want.HealthRequired && health != "") {
			return nil, errors.New("Docker Compose status does not prove every pinned Basement core service")
		}
		observedHealth := "not-configured"
		if want.HealthRequired {
			observedHealth = "healthy"
		}
		result = append(result, BasementCoreServiceObservation{
			Ref: want.Ref, ImageRef: want.ImageRef, ImageDigest: want.ImageDigest,
			Status: "running", Health: observedHealth,
		})
		delete(expectedByRef, row.Service)
	}
	if len(expectedByRef) != 0 {
		return nil, errors.New("Docker Compose status omitted a Basement core service")
	}
	return result, nil
}

type osBasementCoreProber struct{}

func (osBasementCoreProber) Probe(ctx context.Context, expectation BasementCoreHealthExpectation) error {
	timeout := 30 * time.Second
	if expectation.Kind == "tcp" {
		dialer := net.Dialer{Timeout: timeout}
		connection, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", expectation.Port))
		if err != nil {
			return err
		}
		return connection.Close()
	}
	if expectation.Kind != "http" {
		return errors.New("unsupported Basement core health kind")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", expectation.Port, expectation.Path), nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if !slices.Contains(expectation.ExpectedStatuses, response.StatusCode) {
		return errors.New("unexpected local health status")
	}
	return nil
}

var (
	_ BasementCoreOperations    = (*osBasementCoreOperations)(nil)
	_ basementCoreProcessRunner = osBasementCoreProcessRunner{}
	_ basementCoreProber        = osBasementCoreProber{}
	_ basementOwnerIdentity     = localBasementOwnerIdentity{}
)

func (i localBasementOwnerIdentity) Realize(ctx context.Context) (localevidence.OwnerRuntimeBinding, error) {
	result, err := i.service.Realize(ctx)
	return result.Binding, err
}

func (i localBasementOwnerIdentity) Verify(ctx context.Context) (localevidence.OwnerRuntimeBinding, error) {
	return i.service.Verify(ctx)
}
