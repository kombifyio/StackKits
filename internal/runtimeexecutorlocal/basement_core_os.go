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
	"github.com/kombifyio/stackkits/internal/rollout"
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
	message := "verified Basement runtime differs from the authorized project: " + err.code
	var processErr *basementCoreProcessError
	if errors.As(err.cause, &processErr) {
		return message + " (" + processErr.Error() + ")"
	}
	var bounded *basementCoreBoundedDiagnostic
	if errors.As(err.cause, &bounded) && bounded.detail != "" {
		return message + " (" + bounded.detail + ")"
	}
	return message
}

// basementCoreBoundedDiagnostic carries operator-facing detail that already
// passed the bounded, redacted diagnostic helper. A drift message exposes only
// this kind and closed-contract process output; every other cause stays
// private, so an adapter cannot widen the diagnostic surface by accident.
type basementCoreBoundedDiagnostic struct {
	detail string
	cause  error
}

func (err *basementCoreBoundedDiagnostic) Error() string { return err.detail }

func (err *basementCoreBoundedDiagnostic) Unwrap() error { return err.cause }

func newBasementCoreBoundedDiagnostic(cause error, detail string) *basementCoreBoundedDiagnostic {
	return &basementCoreBoundedDiagnostic{
		detail: boundedBasementCoreProcessDiagnostic([]byte(detail)),
		cause:  cause,
	}
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
	if _, ok := basementCoreProjectProfile(project); !ok {
		return BasementCoreApplyObservation{}, errors.New("Basement core operations do not support the selected local profile")
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspaceRoot); err != nil {
		return BasementCoreApplyObservation{}, fmt.Errorf("verify local Basement runtime custody before Apply: %w", err)
	}
	composePath, err := o.persistCompose(project)
	if err != nil {
		return BasementCoreApplyObservation{}, err
	}
	if _, err := o.runner.Run(ctx, basementCoreComposeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return BasementCoreApplyObservation{}, fmt.Errorf("local Docker Compose Apply did not complete: %w", err)
	}
	binding, err := o.ownerIdentity.Realize(ctx)
	if err != nil {
		return BasementCoreApplyObservation{}, fmt.Errorf("local PocketID owner realization did not complete: %w", err)
	}
	// Owner realization creates the exact PocketID OIDC client and installs its
	// one-time secret as a private optional env override. Reconcile Compose once
	// more so TinyAuth runs with that bound credential before Apply can succeed.
	if _, err := o.runner.Run(ctx, basementCoreComposeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return BasementCoreApplyObservation{}, fmt.Errorf("local TinyAuth PocketID binding did not complete: %w", err)
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
	if _, ok := basementCoreProjectProfile(project); !ok {
		return BasementCoreVerifyObservation{}, errors.New("Basement core operations do not support the selected local profile")
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
	probeFailures := make([]error, 0)
	failedRequirementIDs := make([]string, 0)
	for _, expectation := range project.Health {
		// Container health is proven by the exact pinned Compose service
		// observation above; HTTP and TCP contracts require direct probes.
		if expectation.Kind != "contract" && expectation.Kind != "container" {
			if err := o.prober.Probe(ctx, expectation); err != nil {
				if isBasementCoreContextTermination(err) {
					return BasementCoreVerifyObservation{}, err
				}
				// Keep probing: one broken contract must not hide the state of
				// the remaining ones, so the drift names every failing probe.
				probeFailures = append(probeFailures, fmt.Errorf("%s: %w", expectation.RequirementID, err))
				failedRequirementIDs = append(failedRequirementIDs, expectation.RequirementID)
				continue
			}
		}
		probes = append(probes, BasementCoreProbeObservation{
			RequirementID: expectation.RequirementID, Status: "healthy",
		})
	}
	if len(probeFailures) != 0 {
		joined := errors.Join(probeFailures...)
		return BasementCoreVerifyObservation{}, basementCoreObservationDrift(
			project.ProjectRef, "runtime-health-changed",
			newBasementCoreBoundedDiagnostic(joined, "unhealthy health contracts: "+strings.Join(failedRequirementIDs, ", ")),
		)
	}
	binding, err := o.ownerIdentity.Verify(ctx)
	if err != nil {
		return BasementCoreVerifyObservation{}, fmt.Errorf("local PocketID owner verification did not complete: %w", err)
	}
	return BasementCoreVerifyObservation{
		ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest,
		OwnerRef: binding.OwnerRef, PocketIDSubject: binding.PocketIDSubject,
		OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding),
		Status:             "ready", Services: services, Probes: probes,
	}, nil
}

func basementCoreProjectProfile(project BasementCoreProject) (closedLocalCoreExecutionProfile, bool) {
	moduleRef := project.ModuleRef
	if moduleRef == "" {
		moduleRef = basementCoreModuleRef
	}
	return basementClosedLocalCoreExecutionProfileForModule(moduleRef)
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

type basementCoreProcessError struct {
	output string
	cause  error
}

func (err *basementCoreProcessError) Error() string {
	return fmt.Sprintf("docker-compose-exit; output=%q", err.output)
}

func (err *basementCoreProcessError) Unwrap() error { return err.cause }

func boundedBasementCoreProcessDiagnostic(output []byte) string {
	value := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 32 && r <= 126) {
			return r
		}
		return '?'
	}, string(output))
	value = rollout.Redact(strings.TrimSpace(value))
	if len(value) > 512 {
		value = value[:512] + "..."
	}
	return value
}

func (osBasementCoreProcessRunner) Run(ctx context.Context, args []string, directory string, environment []string) ([]byte, error) {
	if len(args) < 6 || args[0] != "compose" || args[1] != "--project-name" ||
		args[2] != "stackkit-basement-core" || args[3] != "-f" ||
		filepath.Clean(args[4]) != args[4] || filepath.Base(args[4]) != "compose.yaml" ||
		filepath.Dir(args[4]) != directory ||
		(!slices.Equal(args[5:], []string{"up", "-d", "--wait", "--wait-timeout", "600"}) &&
			!slices.Equal(args[5:], []string{"ps", "--all", "--format", "json"})) {
		return nil, &basementCoreProcessError{output: "closed-contract-rejected", cause: errors.New("invalid process contract")}
	}
	executable, err := exec.LookPath("docker")
	if err != nil {
		return nil, &basementCoreProcessError{output: "docker-not-found", cause: err}
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	output := &basementCoreBoundedBuffer{remaining: basementCoreProcessOutputLimit}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, &basementCoreProcessError{output: boundedBasementCoreProcessDiagnostic(output.Bytes()), cause: err}
	}
	if output.exceeded {
		return nil, &basementCoreProcessError{output: "output-exceeded", cause: errors.New("bounded process output exceeded")}
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
	result, pending, err := inspectBasementCoreComposeStatus(raw, expected)
	if err != nil {
		return nil, err
	}
	if len(pending) != 0 {
		return nil, errors.New("Docker Compose status does not prove every pinned Basement core service")
	}
	return result, nil
}

func inspectBasementCoreComposeStatus(raw []byte, expected []BasementCoreServiceExpectation) ([]BasementCoreServiceObservation, []string, error) {
	var rows []basementCoreComposePS
	if err := json.Unmarshal(raw, &rows); err != nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var row basementCoreComposePS
			if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, nil, errors.New("Docker Compose status is not bounded JSON")
			}
			rows = append(rows, row)
		}
	}
	if len(rows) != len(expected) {
		return nil, nil, errors.New("Docker Compose status does not contain the exact Basement core service set")
	}
	expectedByRef := make(map[string]BasementCoreServiceExpectation, len(expected))
	for _, service := range expected {
		expectedByRef[service.Ref] = service
	}
	result := make([]BasementCoreServiceObservation, 0, len(rows))
	pending := make([]string, 0, len(rows))
	for _, row := range rows {
		want, ok := expectedByRef[row.Service]
		wantImage := want.ImageRef + "@" + want.ImageDigest
		health := strings.ToLower(strings.TrimSpace(row.Health))
		if !ok || row.Image != wantImage {
			return nil, nil, errors.New("Docker Compose status differs from the pinned Basement core service set")
		}
		delete(expectedByRef, row.Service)
		if strings.ToLower(row.State) != "running" || (want.HealthRequired && health != "healthy") || (!want.HealthRequired && health != "") {
			pending = append(pending, "service:"+want.Ref)
			continue
		}
		observedHealth := "not-configured"
		if want.HealthRequired {
			observedHealth = "healthy"
		}
		result = append(result, BasementCoreServiceObservation{
			Ref: want.Ref, ImageRef: want.ImageRef, ImageDigest: want.ImageDigest,
			Status: "running", Health: observedHealth,
		})
	}
	if len(expectedByRef) != 0 {
		return nil, nil, errors.New("Docker Compose status omitted a Basement core service")
	}
	return result, pending, nil
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
