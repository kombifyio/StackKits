package coolify

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PlatformSecretEnvFile matches nativehost's owner-only secret env_file
// convention (internal/runtimeexecutor/nativehost/coolify_secret_environment.go)
// so Compose stacks read secrets by absolute node path without storing secret
// values in Coolify's API or PostgreSQL database.
const (
	PlatformSecretEnvFile = ".env.platform"
	PlatformSecretEnvMode = os.FileMode(0o600)
)

var envVarKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RenderVaultEnvFile writes a sorted, single-quoted, mode-0600 `.env.platform`
// file atomically inside dir from vault secret references and their resolved
// node-custody values, returning the absolute file path to attach via Compose
// `env_file`.
func RenderVaultEnvFile(dir string, secretRefs map[string]string, resolvedValues map[string]string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("coolify vault secrets: target directory is required")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	envPath := filepath.Join(absDir, PlatformSecretEnvFile)
	if len(secretRefs) == 0 {
		if rmErr := os.Remove(envPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return "", rmErr
		}
		return "", nil
	}

	keys := make([]string, 0, len(secretRefs))
	for key := range secretRefs {
		if !envVarKeyPattern.MatchString(key) {
			return "", fmt.Errorf("coolify vault secrets: invalid environment variable key %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, key := range keys {
		ref := strings.TrimSpace(secretRefs[key])
		if ref == "" {
			return "", fmt.Errorf("coolify vault secrets: empty vault reference for %s", key)
		}
		val, ok := resolvedValues[ref]
		if !ok {
			return "", fmt.Errorf("coolify vault secrets: missing resolved secret for %s (ref %q)", key, ref)
		}
		if strings.ContainsAny(val, "'\r\n\x00") {
			return "", fmt.Errorf("coolify vault secrets: secret for %s contains characters that cannot be safely single-quoted", key)
		}
		fmt.Fprintf(&buf, "%s='%s'\n", key, val)
	}

	if err := writeOwnerEnvFileAtomic(envPath, buf.Bytes()); err != nil {
		return "", err
	}
	return envPath, nil
}

// AttachVaultEnvFileToCompose attaches an absolute `.env.platform` path to
// ONLY the explicitly listed targetServices' `env_file` lists in composeYAML
// so Coolify never stores the underlying secret values in its API or database
// and secrets are never broadcast to unrelated services or sidecars.
//
// It fails closed when targetServices is empty, contains blank names, or names
// any service that does not exist in composeYAML.
func AttachVaultEnvFileToCompose(composeYAML []byte, envFilePath string, targetServices []string) ([]byte, error) {
	if strings.TrimSpace(envFilePath) == "" || !filepath.IsAbs(envFilePath) || filepath.Base(envFilePath) != PlatformSecretEnvFile {
		return nil, fmt.Errorf("coolify vault secrets: env_file path %q must be an absolute path ending in %s", envFilePath, PlatformSecretEnvFile)
	}
	if len(targetServices) == 0 {
		return nil, errors.New("coolify vault secrets: at least one explicit target service is required")
	}
	uniqueTargets := make([]string, 0, len(targetServices))
	seenTargets := make(map[string]struct{}, len(targetServices))
	for _, rawTarget := range targetServices {
		target := strings.TrimSpace(rawTarget)
		if target == "" {
			return nil, errors.New("coolify vault secrets: target service name must not be empty")
		}
		if _, dup := seenTargets[target]; dup {
			continue
		}
		seenTargets[target] = struct{}{}
		uniqueTargets = append(uniqueTargets, target)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(composeYAML, &doc); err != nil {
		return nil, err
	}
	services, ok := doc["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return nil, errors.New("coolify vault secrets: compose YAML defines no services")
	}
	// Pre-validate every target service exists and is a YAML mapping before mutating.
	for _, name := range uniqueTargets {
		rawSvc, exists := services[name]
		if !exists {
			return nil, fmt.Errorf("coolify vault secrets: target service %q does not exist in compose document", name)
		}
		if _, ok := rawSvc.(map[string]any); !ok {
			return nil, fmt.Errorf("coolify vault secrets: service %q is malformed", name)
		}
	}

	for _, name := range uniqueTargets {
		svc := services[name].(map[string]any)
		var files []any
		switch existing := svc["env_file"].(type) {
		case nil:
		case []any:
			files = existing
		case string:
			files = []any{existing}
		default:
			return nil, fmt.Errorf("coolify vault secrets: service %q has unsupported env_file type %T", name, existing)
		}
		alreadyPresent := false
		for _, f := range files {
			switch item := f.(type) {
			case string:
				if item == envFilePath {
					alreadyPresent = true
				}
			case map[string]any:
				if p, ok := item["path"].(string); ok && p == envFilePath {
					alreadyPresent = true
				}
			}
			if alreadyPresent {
				break
			}
		}
		if !alreadyPresent {
			svc["env_file"] = append(files, envFilePath)
		}
	}
	return yaml.Marshal(doc)
}

func writeOwnerEnvFileAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(PlatformSecretEnvMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// CheckpointReason classifies why an atomic Coolify Kopia checkpoint is taken.
type CheckpointReason string

const (
	CheckpointReasonPreUpgrade  CheckpointReason = "pre-upgrade"
	CheckpointReasonPreRotation CheckpointReason = "pre-rotation"
)

// KopiaCheckpointDescriptor describes the mandatory atomic Kopia checkpoint
// required before a Coolify upgrade or secret rotation (ADR-0049 amended).
// Because Coolify migrates PostgreSQL forward on startup and encrypts database
// fields with APP_KEY, Postgres + APP_KEY + API token custody MUST be captured
// and restored together atomically.
type KopiaCheckpointDescriptor struct {
	ID                   string
	Reason               CheckpointReason
	CreatedAt            time.Time
	CoolifyVersion       string
	IncludesPostgresDump bool
	PostgresVolumeRef    string
	IncludesAppKey       bool
	AppKeyCustodyRef     string
	IncludesAPIToken     bool
	APITokenCustodyRef   string
	AtomicBundle         bool
}

// Validate enforces fail-closed that the Kopia checkpoint captures Postgres,
// APP_KEY custody, and API token custody together as one atomic bundle without
// embedding raw secret material.
func (d KopiaCheckpointDescriptor) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("coolify checkpoint: snapshot ID is required")
	}
	if d.Reason != CheckpointReasonPreUpgrade && d.Reason != CheckpointReasonPreRotation {
		return fmt.Errorf("coolify checkpoint: unsupported reason %q", d.Reason)
	}
	if d.CreatedAt.IsZero() {
		return errors.New("coolify checkpoint: creation timestamp is required")
	}
	if strings.TrimSpace(d.CoolifyVersion) == "" {
		return errors.New("coolify checkpoint: Coolify version is required")
	}
	if !d.AtomicBundle {
		return errors.New("coolify checkpoint: Postgres, APP_KEY, and API token must be captured together in one atomic Kopia bundle (ADR-0049)")
	}
	if !d.IncludesPostgresDump || strings.TrimSpace(d.PostgresVolumeRef) == "" {
		return errors.New("coolify checkpoint: Postgres dump and volume reference are required")
	}
	if !d.IncludesAppKey || strings.TrimSpace(d.AppKeyCustodyRef) == "" {
		return errors.New("coolify checkpoint: APP_KEY custody reference is required")
	}
	if strings.HasPrefix(strings.TrimSpace(d.AppKeyCustodyRef), "base64:") {
		return errors.New("coolify checkpoint: AppKeyCustodyRef must reference custody storage, not contain raw APP_KEY material")
	}
	if !d.IncludesAPIToken || strings.TrimSpace(d.APITokenCustodyRef) == "" {
		return errors.New("coolify checkpoint: API token custody reference is required")
	}
	if strings.Contains(d.APITokenCustodyRef, "|") {
		return errors.New("coolify checkpoint: APITokenCustodyRef must reference custody storage, not contain a raw Sanctum API token")
	}
	return nil
}

// LiteralVarRotationPhase tracks the strict atomic two-phase rotation contract
// for Git/Nixpacks `is_literal` variables in Coolify:
//
//	write-new-generation -> deploy-and-verify-healthy -> revoke-old
type LiteralVarRotationPhase string

const (
	RotationPhasePlanned                LiteralVarRotationPhase = "planned"
	RotationPhaseWriteNewGeneration     LiteralVarRotationPhase = "write-new-generation"
	RotationPhaseDeployAndVerifyHealthy LiteralVarRotationPhase = "deploy-and-verify-healthy"
	RotationPhaseRevokeOld              LiteralVarRotationPhase = "revoke-old"
)

// LiteralVarRotationPlan governs atomic two-phase rotation of a Git/Nixpacks
// `is_literal` variable backed by a verified pre-rotation Kopia checkpoint.
type LiteralVarRotationPlan struct {
	ResourceUUID       string
	VarKey             string
	IsLiteral          bool
	PreviousGeneration string
	NextGeneration     string
	Checkpoint         KopiaCheckpointDescriptor
	Phase              LiteralVarRotationPhase
	DeployHealthy      bool
	OldRevoked         bool
}

// NewLiteralVarRotationPlan validates inputs and creates a two-phase rotation
// state machine for a Git/Nixpacks `is_literal` variable.
func NewLiteralVarRotationPlan(resourceUUID, varKey, prevGen, nextGen string, isLiteral bool, checkpoint KopiaCheckpointDescriptor) (*LiteralVarRotationPlan, error) {
	if strings.TrimSpace(resourceUUID) == "" {
		return nil, errors.New("coolify literal rotation: resource_uuid is required")
	}
	if !envVarKeyPattern.MatchString(varKey) {
		return nil, fmt.Errorf("coolify literal rotation: invalid variable key %q", varKey)
	}
	if !isLiteral {
		return nil, errors.New("coolify literal rotation: two-phase literal rotation applies only to Git/Nixpacks is_literal=true variables (Compose stacks use .env.platform)")
	}
	if strings.TrimSpace(prevGen) == "" || strings.TrimSpace(nextGen) == "" || prevGen == nextGen {
		return nil, errors.New("coolify literal rotation: distinct non-empty previous and next generation IDs are required")
	}
	if checkpoint.Reason != CheckpointReasonPreRotation {
		return nil, fmt.Errorf("coolify literal rotation: checkpoint reason must be %q", CheckpointReasonPreRotation)
	}
	if err := checkpoint.Validate(); err != nil {
		return nil, fmt.Errorf("coolify literal rotation: invalid pre-rotation checkpoint: %w", err)
	}
	return &LiteralVarRotationPlan{
		ResourceUUID:       strings.TrimSpace(resourceUUID),
		VarKey:             varKey,
		IsLiteral:          true,
		PreviousGeneration: prevGen,
		NextGeneration:     nextGen,
		Checkpoint:         checkpoint,
		Phase:              RotationPhasePlanned,
	}, nil
}

// MarkNewGenerationWritten transitions from planned -> write-new-generation.
func (p *LiteralVarRotationPlan) MarkNewGenerationWritten() error {
	if p.Phase != RotationPhasePlanned {
		return fmt.Errorf("coolify literal rotation: cannot write new generation from phase %q", p.Phase)
	}
	p.Phase = RotationPhaseWriteNewGeneration
	return nil
}

// MarkDeployedAndHealthy transitions from write-new-generation ->
// deploy-and-verify-healthy only when healthy is true.
func (p *LiteralVarRotationPlan) MarkDeployedAndHealthy(healthy bool) error {
	if p.Phase != RotationPhaseWriteNewGeneration {
		return fmt.Errorf("coolify literal rotation: cannot verify deploy from phase %q (must write new generation first)", p.Phase)
	}
	if !healthy {
		return errors.New("coolify literal rotation: deployment is not healthy; refusing to advance rotation or revoke old generation")
	}
	p.DeployHealthy = true
	p.Phase = RotationPhaseDeployAndVerifyHealthy
	return nil
}

// MarkOldRevoked transitions from deploy-and-verify-healthy -> revoke-old.
// It fails closed if the new generation has not been deployed and verified
// healthy.
func (p *LiteralVarRotationPlan) MarkOldRevoked() error {
	if p.Phase != RotationPhaseDeployAndVerifyHealthy || !p.DeployHealthy {
		return errors.New("coolify literal rotation: refusing to revoke old generation before new generation is deployed and verified healthy")
	}
	p.OldRevoked = true
	p.Phase = RotationPhaseRevokeOld
	return nil
}
