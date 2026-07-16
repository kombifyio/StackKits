package architecturev2renderer

import "github.com/kombifyio/stackkits/internal/generationartifact"

// InstallOptions controls an Architecture v2 managed-output transaction.
// WorkspaceRoot must already exist and may not be a symlink. GeneratedAt is
// optional receipt metadata and must use RFC3339 when provided.
type InstallOptions struct {
	WorkspaceRoot string
	GeneratedAt   string
}

// InstallResult exposes the exact manifest and receipt installed with the
// managed output root. Committed remains true when installation succeeded but
// best-effort transaction cleanup subsequently failed.
type InstallResult struct {
	Committed  bool
	OutputRoot string
	Manifest   generationartifact.ArtifactManifest
	Receipt    generationartifact.GenerationReceipt
}
