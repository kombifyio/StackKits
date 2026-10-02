package opentofu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const (
	openTofuEncryptionVersion = "v0"
	maxOpenTofuStateBytes     = 128 << 20
	maxOpenTofuStateListBytes = 4 << 20
)

var publicStateAddressPattern = regexp.MustCompile(`^(?:module\.[A-Za-z0-9_-]+\.)*(?:data\.)?[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// StateInspection is the secret-free result of opening one encrypted local
// backend through its Owner custody. Resources contains only closed resource
// addresses; no state values or OpenTofu diagnostics leave the executor.
type StateInspection struct {
	Root        string   `json:"root"`
	StateSHA256 string   `json:"stateSha256"`
	Resources   []string `json:"resources"`
}

// ExpectedStateRoot is the signed Apply identity of one OpenTofu root that
// must exist during live verification. It is deliberately narrower than a
// filesystem discovery result: stale roots cannot add themselves to this set.
type ExpectedStateRoot struct {
	Root        string
	ModuleRef   string
	InstanceRef string
}

// tofuRun is the recorded outcome of one init, plan, and apply of a root.
type tofuRun struct {
	Init        stepRecord  `json:"init"`
	Plan        planRecord  `json:"plan"`
	Apply       *stepRecord `json:"apply,omitempty"`
	StateSHA256 string      `json:"stateSha256"`
}

type rootFile struct {
	name string
	data []byte
	mode os.FileMode
}

// packagedTools resolves the packaged tofu binary and provider mirror and
// fails closed before anything is written when either is missing.
type providerClosure interface {
	Directory() string
	LockForConfiguration([]byte) ([]byte, error)
}

func (r Runtime) packagedTools() (string, providerClosure, error) {
	binary := r.Binary
	if binary == "" {
		packaged, ok := tofu.PackagedBinaryPath()
		if !ok {
			return "", nil, errors.New("the packaged OpenTofu binary is missing: reinstall the StackKit release archive, which ships tofu beside stackkit, or set STACKKIT_TOFU_BINARY")
		}
		binary = packaged
	}
	providers := r.ProvidersDir
	if providers == "" {
		providers, _ = tofu.PackagedProvidersDir()
	}
	load := r.providerLoader
	if load == nil {
		load = func(directory string) (providerClosure, error) { return tofu.LoadProviderClosure(directory) }
	}
	closure, err := load(providers)
	if err != nil {
		return "", nil, err
	}
	return binary, closure, nil
}

// ensureRootDir creates the workspace-relative root as a private plain
// directory below .stackkit/runtime.
func ensureRootDir(workspace, relative string) (string, error) {
	current := workspace
	for index, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		mode := os.FileMode(0o750)
		if index >= 2 {
			mode = 0o700
		}
		if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create OpenTofu root %s: %w", relative, err)
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("OpenTofu root path %s is not a plain directory", current)
		}
	}
	if err := os.Chmod(current, 0o700); err != nil {
		return "", fmt.Errorf("restrict OpenTofu root: %w", err)
	}
	return current, nil
}

// writeRoot writes the root marker, the offline CLI configuration, and the
// root files.
func writeRoot(root string, marker RootMarker, providers string, lock []byte, files ...rootFile) error {
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	all := append([]rootFile{
		{MarkerFile, append(encoded, '\n'), 0o600},
		{CLIConfigFile, tofu.OfflineCLIConfig(providers), 0o600},
		{LockFile, lock, 0o640},
	}, files...)
	for _, file := range all {
		if err := writeFileAtomic(root, file.name, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

// runRoot runs `tofu init`, `plan -detailed-exitcode -out=tfplan`, and, only
// when the plan has changes, `apply tfplan` offline in root. It never
// refreshes only, replaces, or destroys; stale and newly saved plans are
// removed on every exit and the local-backend state is Owner-encrypted.
func (r Runtime) runRoot(ctx context.Context, portableRoot, root, binary string, environment []string) (tofuRun, error) {
	var run tofuRun
	err := r.withStateCustody(portableRoot, func(key []byte) error {
		var err error
		run, err = r.runRootWithKey(ctx, root, binary, environment, key)
		return err
	})
	return run, err
}

func (r Runtime) withStateCustody(portableRoot string, use func([]byte) error) error {
	return localevidence.WithOpenTofuStateKey(r.WorkspaceRoot, portableRoot, use)
}

// InspectStates opens the exact roots named by the signed current Apply result
// with the same packaged binary, offline provider closure, and per-root Owner
// key used by Apply. It refuses missing or unconstrained current roots and
// never reports inactive historical roots left for rollback. Each address
// list is bound to the captured ciphertext bytes before returning.
func (r Runtime) InspectStates(ctx context.Context, expected []ExpectedStateRoot) ([]StateInspection, error) {
	if ctx == nil {
		return nil, errors.New("OpenTofu state inspection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(expected) == 0 {
		return nil, errors.New("the signed Apply result identifies no OpenTofu state roots")
	}
	expectedByRoot := make(map[string]ExpectedStateRoot, len(expected))
	for _, item := range expected {
		if item.Root == "" || item.Root != path.Clean(item.Root) || filepath.IsAbs(item.Root) ||
			strings.TrimSpace(item.ModuleRef) == "" || strings.TrimSpace(item.InstanceRef) == "" {
			return nil, errors.New("the signed Apply result contains an invalid OpenTofu state root")
		}
		if _, duplicate := expectedByRoot[item.Root]; duplicate {
			return nil, errors.New("the signed Apply result repeats an OpenTofu state root")
		}
		expectedByRoot[item.Root] = item
	}
	roots := make([]string, 0, len(expectedByRoot))
	for portableRoot, item := range expectedByRoot {
		if err := requirePlainRelativeDirectory(r.WorkspaceRoot, portableRoot); err != nil {
			return nil, fmt.Errorf("inspect OpenTofu state root %s: %w", portableRoot, err)
		}
		marker, err := ReadRootMarker(filepath.Join(r.WorkspaceRoot, filepath.FromSlash(portableRoot)))
		if err != nil {
			return nil, fmt.Errorf("inspect OpenTofu state root %s: %w", portableRoot, err)
		}
		if marker.ModuleRef != item.ModuleRef || marker.InstanceRef != item.InstanceRef {
			return nil, fmt.Errorf("OpenTofu state root %s does not match the signed Apply identity", portableRoot)
		}
		roots = append(roots, portableRoot)
	}
	sort.Strings(roots)
	binary, providers, err := r.packagedTools()
	if err != nil {
		return nil, err
	}
	inspections := make([]StateInspection, len(roots))
	for index, portableRoot := range roots {
		inspection, err := r.inspectState(ctx, portableRoot, binary, providers.Directory())
		if err != nil {
			return nil, fmt.Errorf("inspect OpenTofu state root %s: %w", portableRoot, err)
		}
		inspections[index] = inspection
	}
	return inspections, nil
}

func requirePlainRelativeDirectory(root, relative string) error {
	components := strings.Split(relative, "/")
	if len(components) < 4 || components[0] != ".stackkit" || components[1] != "runtime" || components[len(components)-1] != RootDirName {
		return errors.New("OpenTofu root is outside the governed runtime layout")
	}
	current := filepath.Clean(root)
	if err := requirePlainDirectory(current); err != nil {
		return err
	}
	for _, component := range components {
		if component == "" || component != filepath.Base(component) {
			return errors.New("OpenTofu directory chain contains an invalid component")
		}
		current = filepath.Join(current, component)
		if err := requirePlainDirectory(current); err != nil {
			return err
		}
	}
	return nil
}

func requirePlainDirectory(filename string) error {
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a plain directory", filename)
	}
	return nil
}

func (r Runtime) inspectState(ctx context.Context, portableRoot, binary, providers string) (StateInspection, error) {
	root := filepath.Join(r.WorkspaceRoot, filepath.FromSlash(portableRoot))
	cliConfig, exists, err := readBoundedRegularFile(filepath.Join(root, CLIConfigFile), 1<<20)
	if err != nil || !exists || !bytes.Equal(cliConfig, tofu.OfflineCLIConfig(providers)) {
		clear(cliConfig)
		return StateInspection{}, errors.New("current OpenTofu root is not bound to the packaged offline provider closure")
	}
	clear(cliConfig)
	statePath := filepath.Join(root, StateFile)
	before, exists, err := readBoundedRegularFile(statePath, maxOpenTofuStateBytes)
	if err != nil || !exists || !isOpenTofuEncryptedPayload(before) {
		clear(before)
		return StateInspection{}, errors.New("current OpenTofu state is not bounded Owner-encrypted ciphertext")
	}
	beforeDigest := digestBytes(before)
	defer clear(before)

	var resources []string
	err = r.withStateCustody(portableRoot, func(key []byte) error {
		probe, err := os.MkdirTemp("", "stackkit-state-inspection-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(probe)
		if err := os.Chmod(probe, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(probe, StateFile), before, 0o600); err != nil {
			return err
		}
		config := filepath.Join(probe, CLIConfigFile)
		if err := os.WriteFile(config, tofu.OfflineCLIConfig(providers), 0o600); err != nil {
			return err
		}
		options := []tofu.ExecutorOption{
			tofu.WithWorkDir(probe), tofu.WithBinary(binary),
			tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...), tofu.WithoutEnvPrefix("TF_LOG", "TF_CLI_ARGS_"),
			tofu.WithEnv(
				"TF_CLI_CONFIG_FILE="+config,
				"TF_CLI_ARGS=-no-color",
				"TF_ENCRYPTION="+openTofuEncryptionConfig(key, false),
			),
		}
		if r.Timeout > 0 {
			options = append(options, tofu.WithTimeout(r.Timeout))
		}
		result, commandErr := tofu.NewExecutor(options...).State(ctx)
		if err := requireTofuStep("state inspection", result, commandErr); err != nil {
			discardTofuOutput(result)
			return errors.New("current OpenTofu state cannot be inspected by Owner custody")
		}
		if len(result.Stdout) == 0 || len(result.Stdout) > maxOpenTofuStateListBytes {
			discardTofuOutput(result)
			return errors.New("current OpenTofu state has no bounded resource address list")
		}
		for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
			address := strings.TrimSpace(line)
			if !publicStateAddressPattern.MatchString(address) {
				discardTofuOutput(result)
				return errors.New("current OpenTofu state contains a resource address outside the public boundary")
			}
			resources = append(resources, address)
		}
		discardTofuOutput(result)
		if len(resources) == 0 {
			return errors.New("current OpenTofu state has no resources")
		}
		sort.Strings(resources)
		for index := 1; index < len(resources); index++ {
			if resources[index] == resources[index-1] {
				return errors.New("current OpenTofu state repeats a resource address")
			}
		}
		return nil
	})
	if err != nil {
		return StateInspection{}, err
	}
	after, exists, err := readBoundedRegularFile(statePath, maxOpenTofuStateBytes)
	if err != nil || !exists || !isOpenTofuEncryptedPayload(after) {
		clear(after)
		return StateInspection{}, errors.New("current OpenTofu state changed outside the encrypted boundary")
	}
	afterDigest := digestBytes(after)
	clear(after)
	if afterDigest != beforeDigest {
		return StateInspection{}, errors.New("current OpenTofu state changed during inspection")
	}
	return StateInspection{Root: portableRoot, StateSHA256: beforeDigest, Resources: resources}, nil
}

func (r Runtime) requireStateCustody(ctx context.Context, portableRoot, binary, providers string) error {
	return r.withStateCustody(portableRoot, func(key []byte) error {
		return r.preflightStateCustody(ctx, filepath.Join(r.WorkspaceRoot, filepath.FromSlash(portableRoot)), binary, providers, key)
	})
}

// Validate every existing ciphertext before a native preparation can mutate
// runtime files or identities. Only ciphertext enters the isolated backend;
// legacy plaintext stays in place until the bounded migration below.
func (r Runtime) preflightStateCustody(ctx context.Context, root, binary, providers string, key []byte) error {
	if ctx == nil {
		return errors.New("OpenTofu state preflight requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, name := range []string{StateFile, StateFile + ".backup"} {
		data, exists, err := readBoundedRegularFile(filepath.Join(root, name), maxOpenTofuStateBytes)
		if err != nil {
			return fmt.Errorf("inspect existing OpenTofu state: %w", err)
		}
		if !exists {
			continue
		}
		if !isOpenTofuEncryptedPayload(data) {
			var header struct {
				Version           int             `json:"version"`
				EncryptionVersion json.RawMessage `json:"encryption_version"`
			}
			valid := json.Unmarshal(data, &header) == nil && header.Version == 4 && len(header.EncryptionVersion) == 0
			clear(data)
			if !valid {
				return errors.New("existing OpenTofu state is invalid")
			}
			continue
		}
		err = func() error {
			probe, err := os.MkdirTemp("", "stackkit-state-preflight-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(probe)
			if err := os.WriteFile(filepath.Join(probe, StateFile), data, 0o600); err != nil {
				return err
			}
			config := filepath.Join(probe, CLIConfigFile)
			if err := os.WriteFile(config, tofu.OfflineCLIConfig(providers), 0o600); err != nil {
				return err
			}
			options := []tofu.ExecutorOption{tofu.WithWorkDir(probe), tofu.WithBinary(binary),
				tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...), tofu.WithoutEnvPrefix("TF_LOG", "TF_CLI_ARGS_"),
				tofu.WithEnv("TF_CLI_CONFIG_FILE="+config, "TF_ENCRYPTION="+openTofuEncryptionConfig(key, false))}
			if r.Timeout > 0 {
				options = append(options, tofu.WithTimeout(r.Timeout))
			}
			result, commandErr := tofu.NewExecutor(options...).State(ctx)
			verificationErr := requireTofuStep("existing state custody verification", result, commandErr)
			discardTofuOutput(result)
			if verificationErr != nil {
				return errors.New("existing OpenTofu state cannot be opened by current Owner custody")
			}
			return ctx.Err()
		}()
		clear(data)
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

// runRootDestroy destroys every resource of root offline with its current
// configuration: `plan -destroy` and, when it has changes, apply of that
// saved plan. Resources kept by `lifecycle { destroy = false }` (native data
// volumes) are forgotten, not deleted.
func (r Runtime) runRootDestroy(ctx context.Context, portableRoot, root, binary string, environment []string) (tofuRun, error) {
	var run tofuRun
	err := r.withStateCustody(portableRoot, func(key []byte) error {
		var err error
		run, err = r.runRootWithKeyMode(ctx, root, binary, environment, key, true)
		return err
	})
	return run, err
}

func (r Runtime) runRootWithKey(ctx context.Context, root, binary string, environment []string, key []byte) (tofuRun, error) {
	return r.runRootWithKeyMode(ctx, root, binary, environment, key, false)
}

func (r Runtime) runRootWithKeyMode(ctx context.Context, root, binary string, environment []string, key []byte, destroy bool) (tofuRun, error) {
	environment = withoutEnvironment(environment, "TF_ENCRYPTION")
	commonEnvironment := append(append([]string(nil), environment...),
		"TF_CLI_CONFIG_FILE="+filepath.Join(root, CLIConfigFile),
		"TF_CLI_ARGS=-no-color",
	)
	options := func(workDir string, migration bool) []tofu.ExecutorOption {
		values := append(append([]string(nil), commonEnvironment...), "TF_ENCRYPTION="+openTofuEncryptionConfig(key, migration))
		result := []tofu.ExecutorOption{
			tofu.WithWorkDir(workDir), tofu.WithBinary(binary),
			tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...), tofu.WithoutEnvPrefix("TF_LOG", "TF_CLI_ARGS_"), tofu.WithEnv(values...),
		}
		if r.Timeout > 0 {
			result = append(result, tofu.WithTimeout(r.Timeout))
		}
		return result
	}
	planPath := filepath.Join(root, PlanFile)
	if err := removeSavedPlan(planPath); err != nil {
		return tofuRun{}, err
	}
	defer func() { _ = os.Remove(planPath) }()
	if err := migratePlaintextState(ctx, root, options); err != nil {
		return tofuRun{}, err
	}
	runner := tofu.NewExecutor(options(root, false)...)
	var run tofuRun
	initResult, err := runner.InitReadonly(ctx)
	if err := requireTofuStep("init", initResult, err); err != nil {
		discardTofuOutput(initResult)
		return tofuRun{}, err
	}
	run.Init = stepRecord{ExitCode: initResult.ExitCode}
	discardTofuOutput(initResult)
	planResult, err := runner.Plan(ctx, PlanFile, destroy)
	if err := requireTofuStep("plan", planResult, err); err != nil {
		discardTofuOutput(planResult)
		return tofuRun{}, err
	}
	if err := requireEncryptedFile(planPath, "OpenTofu plan"); err != nil {
		return tofuRun{}, err
	}
	changes := tofu.ParsePlanOutput(planResult.Stdout)
	run.Plan = planRecord{ExitCode: planResult.ExitCode, Add: changes.Add, Change: changes.Change, Destroy: changes.Destroy}
	discardTofuOutput(planResult)
	if planResult.ExitCode == 2 {
		apply := runner.Apply
		if destroy {
			apply = runner.ApplySuppressingForgetErrors
		}
		applyResult, err := apply(ctx, PlanFile)
		if err := requireTofuStep("apply", applyResult, err); err != nil {
			discardTofuOutput(applyResult)
			return tofuRun{}, err
		}
		run.Apply = &stepRecord{ExitCode: applyResult.ExitCode}
		discardTofuOutput(applyResult)
	}
	if run.StateSHA256, err = protectState(root); err != nil {
		return tofuRun{}, err
	}
	return run, nil
}

func removeSavedPlan(filename string) error {
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("saved OpenTofu plan is not a regular file")
	}
	if err := os.Remove(filename); err != nil {
		return fmt.Errorf("remove stale OpenTofu plan: %w", err)
	}
	return nil
}

func migratePlaintextState(
	ctx context.Context,
	root string,
	options func(string, bool) []tofu.ExecutorOption,
) error {
	for _, name := range []string{StateFile, StateFile + ".backup"} {
		if err := migratePlaintextStateFile(ctx, root, name, options); err != nil {
			return err
		}
	}
	return nil
}

func migratePlaintextStateFile(
	ctx context.Context,
	root, name string,
	options func(string, bool) []tofu.ExecutorOption,
) error {
	statePath := filepath.Join(root, name)
	state, exists, err := readBoundedRegularFile(statePath, maxOpenTofuStateBytes)
	if err != nil {
		return fmt.Errorf("inspect OpenTofu state for encryption migration: %w", err)
	}
	if !exists || isOpenTofuEncryptedPayload(state) {
		return nil
	}
	defer clear(state)
	if !json.Valid(state) {
		return errors.New("OpenTofu state is neither a valid plaintext state nor an encrypted payload")
	}

	migrationRoot, err := os.MkdirTemp(filepath.Dir(root), ".opentofu-state-encryption-*")
	if err != nil {
		return fmt.Errorf("create private OpenTofu state migration root: %w", err)
	}
	defer func() { _ = os.RemoveAll(migrationRoot) }()
	if err := os.Chmod(migrationRoot, 0o700); err != nil {
		return fmt.Errorf("restrict OpenTofu state migration root: %w", err)
	}

	migrator := tofu.NewExecutor(options(migrationRoot, true)...)
	result, commandErr := migrator.StatePush(ctx, bytes.NewReader(state))
	if err := requireTofuStep("state encryption migration", result, commandErr); err != nil {
		discardTofuOutput(result)
		return err
	}
	discardTofuOutput(result)
	migratedPath := filepath.Join(migrationRoot, StateFile)
	if err := requireEncryptedFile(migratedPath, "migrated OpenTofu state"); err != nil {
		return err
	}

	// The migration-only plaintext fallback is gone before verification. A
	// wrong Owner key or incomplete ciphertext therefore fails before the
	// original state is replaced.
	verifier := tofu.NewExecutor(options(migrationRoot, false)...)
	result, commandErr = verifier.State(ctx)
	verificationErr := requireTofuStep("encrypted state verification", result, commandErr)
	discardTofuOutput(result)
	if verificationErr != nil {
		return errors.New("the migrated OpenTofu state cannot be opened by current Owner custody")
	}
	migrated, exists, err := readBoundedRegularFile(migratedPath, maxOpenTofuStateBytes)
	if err != nil {
		return fmt.Errorf("read migrated OpenTofu state: %w", err)
	}
	if !exists {
		return errors.New("the OpenTofu state migration produced no state")
	}
	defer clear(migrated)
	if err := writeFileAtomic(root, name, migrated, 0o600); err != nil {
		return fmt.Errorf("install encrypted OpenTofu state: %w", err)
	}
	return nil
}

func discardTofuOutput(result *tofu.Result) {
	if result == nil {
		return
	}
	result.Stdout = ""
	result.Stderr = ""
}

func openTofuEncryptionConfig(key []byte, migration bool) string {
	return tofu.StateEncryptionConfig(key, migration)
}

func withoutEnvironment(environment []string, name string) []string {
	filtered := make([]string, 0, len(environment))
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if key != name {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func readBoundedRegularFile(filename string, limit int64) ([]byte, bool, error) {
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, errors.New("path is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > limit {
		return nil, false, fmt.Errorf("file size %d is outside the supported boundary", info.Size())
	}
	data, err := os.ReadFile(filename)
	return data, err == nil, err
}

func isOpenTofuEncryptedPayload(data []byte) bool {
	var envelope struct {
		Version string `json:"encryption_version"`
		Data    []byte `json:"encrypted_data"`
	}
	return json.Unmarshal(data, &envelope) == nil && envelope.Version == openTofuEncryptionVersion && len(envelope.Data) > 0
}

func requireEncryptedFile(filename, label string) error {
	data, exists, err := readBoundedRegularFile(filename, maxOpenTofuStateBytes)
	if err != nil || !exists || !isOpenTofuEncryptedPayload(data) {
		return fmt.Errorf("%s is not an encrypted OpenTofu payload", label)
	}
	return nil
}

func writeFileAtomic(directory, name string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(directory, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", name, err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	_, writeErr := temporary.Write(data)
	chmodErr := temporary.Chmod(mode)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	target := filepath.Join(directory, name)
	if info, err := os.Lstat(target); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("OpenTofu root file %s is not a regular file", name)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return fmt.Errorf("install %s: %w", name, err)
	}
	return nil
}

// protectState restricts the local-backend state to the owner and returns
// its digest. A root without state after a successful run is a failure.
func protectState(root string) (string, error) {
	statePath := filepath.Join(root, StateFile)
	info, err := os.Lstat(statePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("OpenTofu apply left no local-backend state in the root")
	}
	if err := os.Chmod(statePath, 0o600); err != nil {
		return "", fmt.Errorf("restrict OpenTofu state: %w", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return "", fmt.Errorf("read OpenTofu state: %w", err)
	}
	if !isOpenTofuEncryptedPayload(data) {
		return "", errors.New("OpenTofu state is not encrypted by Owner custody")
	}
	backupPath := statePath + ".backup"
	if _, err := os.Lstat(backupPath); err == nil {
		if err := os.Chmod(backupPath, 0o600); err != nil {
			return "", fmt.Errorf("restrict OpenTofu state backup: %w", err)
		}
		if err := requireEncryptedFile(backupPath, "OpenTofu state backup"); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect OpenTofu state backup: %w", err)
	}
	return digestBytes(data), nil
}
