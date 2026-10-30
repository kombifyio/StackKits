// Package failureknowledge holds the offline failure resolution snapshot: a
// versioned SQLite FTS5 database of promoted failure resolutions that ships
// with StackKits releases, and its lookup.
//
// A resolution names normalized failure signatures (fk-normalizer), where it
// applies, the root cause and fix steps as references to StackKits or
// Techstack procedures. The snapshot holds no installation, tenant or subject
// data; it is knowledge, never execution authority. A fix runs only through
// the referenced product procedure.
//
// The snapshot is built from a resolution catalog (schema
// kombify.failure-resolution-catalog/v1) exported from the curated catalog.
// Lookup runs offline: exact signature first, then FTS5 lexical search, and a
// semantic step only when a local Ollama with bge-m3 is configured, against
// the BGE-M3 vectors the snapshot carries.
package failureknowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

const (
	// CatalogSchema is the catalog format the snapshot is built from.
	CatalogSchema = "kombify.failure-resolution-catalog/v1"
	// SnapshotSchema versions the SQLite layout.
	SnapshotSchema = "stackkits.failure-snapshot/v1"
	// SnapshotFileName is the snapshot's name in release archives and beside
	// an installed CLI.
	SnapshotFileName = "failure-resolutions.sqlite"
	// SnapshotEnv overrides where the CLI finds the snapshot.
	SnapshotEnv = "STACKKIT_FAILURE_SNAPSHOT"
	// VectorDimensions is the BGE-M3 dense dimension.
	VectorDimensions = 1024
	vectorEncoding   = "float32le-base64"
	maxRecords       = 5000
)

var (
	signaturePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idPattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	productPattern   = regexp.MustCompile(`^(stackkits|techstack)$`)
	procedurePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/@#-]{1,199}$`)
)

// Symptom is one normalized failure symptom.
type Symptom struct {
	Signature         string `json:"signature"`
	NormalizerVersion string `json:"normalizerVersion,omitempty"`
	Component         string `json:"component,omitempty"`
	Phase             string `json:"phase,omitempty"`
	ErrorCode         string `json:"errorCode,omitempty"`
	Template          string `json:"template,omitempty"`
}

// Applicability lists the contexts a resolution applies to; an empty
// dimension applies everywhere.
type Applicability struct {
	StackKitModules   []string `json:"stackkitModules,omitempty"`
	StackKitVersions  []string `json:"stackkitVersions,omitempty"`
	AppVersions       []string `json:"appVersions,omitempty"`
	OS                []string `json:"os,omitempty"`
	OSVersions        []string `json:"osVersions,omitempty"`
	Arch              []string `json:"arch,omitempty"`
	ContainerRuntimes []string `json:"containerRuntimes,omitempty"`
	Providers         []string `json:"providers,omitempty"`
	DeviceClasses     []string `json:"deviceClasses,omitempty"`
}

// FixStep references a product procedure; it is never a command.
type FixStep struct {
	Product   string `json:"product"`
	Procedure string `json:"procedure"`
	Summary   string `json:"summary,omitempty"`
}

// Evidence is the verified outcome count of a resolution.
type Evidence struct {
	SuccessCount int     `json:"successCount"`
	FailureCount int     `json:"failureCount"`
	SuccessRate  float64 `json:"successRate"`
}

// CatalogRecord is one promoted resolution of the catalog.
type CatalogRecord struct {
	ID             string        `json:"id"`
	Title          string        `json:"title"`
	Symptoms       []Symptom     `json:"symptoms"`
	Applicability  Applicability `json:"applicability"`
	RootCause      string        `json:"rootCause"`
	FixSteps       []FixStep     `json:"fixSteps"`
	Evidence       Evidence      `json:"evidence"`
	Revision       int           `json:"revision"`
	ReviewedAt     *time.Time    `json:"reviewedAt,omitempty"`
	SymptomText    string        `json:"symptomText"`
	BodyText       string        `json:"bodyText"`
	EmbeddingModel string        `json:"embeddingModel,omitempty"`
	Embedding      string        `json:"embedding,omitempty"`
}

// Catalog is the export of promoted resolutions the snapshot is built from.
type Catalog struct {
	Schema         string          `json:"schema"`
	GeneratedAt    time.Time       `json:"generatedAt"`
	Dimensions     int             `json:"dimensions"`
	VectorEncoding string          `json:"vectorEncoding"`
	Truncated      bool            `json:"truncated"`
	Resolutions    []CatalogRecord `json:"resolutions"`
}

// SnapshotInfo describes a built or opened snapshot.
type SnapshotInfo struct {
	Schema             string `json:"schema"`
	Version            string `json:"version"`
	CatalogGeneratedAt string `json:"catalogGeneratedAt"`
	CatalogSHA256      string `json:"catalogSha256"`
	Resolutions        int    `json:"resolutions"`
	Signatures         int    `json:"signatures"`
	Vectors            int    `json:"vectors"`
	EmbeddingModel     string `json:"embeddingModel,omitempty"`
	Path               string `json:"path,omitempty"`
}

// LoadCatalog reads and validates a catalog file and returns it with the
// SHA-256 of its bytes.
func LoadCatalog(path string) (Catalog, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, "", fmt.Errorf("read failure catalog: %w", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return Catalog{}, "", fmt.Errorf("parse failure catalog: %w", err)
	}
	if err := validateCatalog(&catalog); err != nil {
		return Catalog{}, "", err
	}
	digest := sha256.Sum256(raw)
	return catalog, hex.EncodeToString(digest[:]), nil
}

func validateCatalog(catalog *Catalog) error {
	switch {
	case catalog.Schema != CatalogSchema:
		return fmt.Errorf("failure catalog schema is %q, want %q", catalog.Schema, CatalogSchema)
	case catalog.GeneratedAt.IsZero():
		return errors.New("failure catalog has no generatedAt")
	case catalog.Truncated:
		return errors.New("failure catalog is truncated; export it completely")
	case len(catalog.Resolutions) > maxRecords:
		return fmt.Errorf("failure catalog has %d resolutions, at most %d", len(catalog.Resolutions), maxRecords)
	case len(catalog.Resolutions) > 0 && catalog.Dimensions != VectorDimensions:
		return fmt.Errorf("failure catalog dimensions are %d, want %d", catalog.Dimensions, VectorDimensions)
	case len(catalog.Resolutions) > 0 && catalog.VectorEncoding != vectorEncoding:
		return fmt.Errorf("failure catalog vector encoding is %q, want %q", catalog.VectorEncoding, vectorEncoding)
	}
	seen := map[string]bool{}
	for index := range catalog.Resolutions {
		record := &catalog.Resolutions[index]
		if !idPattern.MatchString(record.ID) || seen[record.ID] {
			return fmt.Errorf("failure catalog record %d has a missing or repeated id", index)
		}
		seen[record.ID] = true
		if strings.TrimSpace(record.Title) == "" || len(record.Symptoms) == 0 || len(record.FixSteps) == 0 {
			return fmt.Errorf("failure resolution %s needs a title, symptoms and fix steps", record.ID)
		}
		for _, symptom := range record.Symptoms {
			if !signaturePattern.MatchString(symptom.Signature) {
				return fmt.Errorf("failure resolution %s has an invalid signature", record.ID)
			}
		}
		for _, step := range record.FixSteps {
			if !productPattern.MatchString(step.Product) || !procedurePattern.MatchString(step.Procedure) {
				return fmt.Errorf("failure resolution %s has a fix step that is not a product procedure reference", record.ID)
			}
		}
		if record.Embedding != "" {
			if _, err := decodeVector(record.Embedding); err != nil {
				return fmt.Errorf("failure resolution %s: %w", record.ID, err)
			}
		}
	}
	return nil
}

func decodeVector(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("embedding is not base64: %w", err)
	}
	if len(raw) != 4*VectorDimensions {
		return nil, fmt.Errorf("embedding has %d bytes, want %d", len(raw), 4*VectorDimensions)
	}
	return raw, nil
}

const snapshotDDL = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE resolutions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    root_cause TEXT NOT NULL,
    symptoms_json TEXT NOT NULL,
    applicability_json TEXT NOT NULL,
    fix_steps_json TEXT NOT NULL,
    success_count INTEGER NOT NULL,
    failure_count INTEGER NOT NULL,
    success_rate REAL NOT NULL,
    revision INTEGER NOT NULL,
    reviewed_at TEXT NOT NULL,
    embedding_model TEXT NOT NULL,
    embedding BLOB
) WITHOUT ROWID;
CREATE TABLE resolution_signatures (
    signature TEXT NOT NULL,
    resolution_id TEXT NOT NULL REFERENCES resolutions (id),
    PRIMARY KEY (signature, resolution_id)
) WITHOUT ROWID;
CREATE VIRTUAL TABLE resolutions_fts USING fts5 (
    resolution_id UNINDEXED,
    symptom_text,
    body_text,
    tokenize = 'unicode61 remove_diacritics 2'
);
`

func openDB(dsn string) (*sql.DB, error) {
	return driver.Open(dsn, fts5.Register)
}

// BuildSnapshot writes a new snapshot of the catalog to output. The file is
// written beside output and renamed into place, so a reader never sees a
// partial snapshot.
func BuildSnapshot(ctx context.Context, catalog Catalog, catalogSHA256, output string) (SnapshotInfo, error) {
	if err := validateCatalog(&catalog); err != nil {
		return SnapshotInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return SnapshotInfo{}, fmt.Errorf("create snapshot directory: %w", err)
	}
	temporary := output + ".tmp"
	_ = os.Remove(temporary)
	db, err := openDB("file:" + filepath.ToSlash(temporary))
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("create snapshot: %w", err)
	}
	info, err := writeSnapshot(ctx, db, catalog, catalogSHA256)
	if closeErr := db.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return SnapshotInfo{}, err
	}
	if err := os.Rename(temporary, output); err != nil {
		_ = os.Remove(temporary)
		return SnapshotInfo{}, fmt.Errorf("place snapshot: %w", err)
	}
	info.Path = output
	return info, nil
}

func writeSnapshot(ctx context.Context, db *sql.DB, catalog Catalog, catalogSHA256 string) (SnapshotInfo, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, snapshotDDL); err != nil {
		return SnapshotInfo{}, fmt.Errorf("create snapshot schema: %w", err)
	}
	records := append([]CatalogRecord(nil), catalog.Resolutions...)
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	info := SnapshotInfo{Schema: SnapshotSchema, CatalogGeneratedAt: catalog.GeneratedAt.UTC().Format(time.RFC3339), CatalogSHA256: catalogSHA256}
	models := map[string]bool{}
	for _, record := range records {
		symptoms, _ := json.Marshal(record.Symptoms)
		applicability, _ := json.Marshal(record.Applicability)
		fixSteps, _ := json.Marshal(record.FixSteps)
		reviewedAt := ""
		if record.ReviewedAt != nil {
			reviewedAt = record.ReviewedAt.UTC().Format(time.RFC3339)
		}
		var embedding any
		if record.Embedding != "" {
			raw, err := decodeVector(record.Embedding)
			if err != nil {
				return SnapshotInfo{}, err
			}
			embedding = raw
			models[record.EmbeddingModel] = true
			info.Vectors++
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO resolutions VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.Title, record.RootCause, string(symptoms), string(applicability), string(fixSteps),
			record.Evidence.SuccessCount, record.Evidence.FailureCount, record.Evidence.SuccessRate, record.Revision,
			reviewedAt, record.EmbeddingModel, embedding); err != nil {
			return SnapshotInfo{}, fmt.Errorf("write resolution %s: %w", record.ID, err)
		}
		for _, symptom := range record.Symptoms {
			result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO resolution_signatures VALUES (?, ?)`, symptom.Signature, record.ID)
			if err != nil {
				return SnapshotInfo{}, fmt.Errorf("write signature of %s: %w", record.ID, err)
			}
			if added, _ := result.RowsAffected(); added == 1 {
				info.Signatures++
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO resolutions_fts (resolution_id, symptom_text, body_text) VALUES (?, ?, ?)`,
			record.ID, record.SymptomText, record.BodyText); err != nil {
			return SnapshotInfo{}, fmt.Errorf("index resolution %s: %w", record.ID, err)
		}
	}
	info.Resolutions = len(records)
	modelNames := make([]string, 0, len(models))
	for model := range models {
		modelNames = append(modelNames, model)
	}
	sort.Strings(modelNames)
	info.EmbeddingModel = strings.Join(modelNames, ",")
	// The version names the catalog it was built from, so two builds of the
	// same catalog carry the same version.
	short := catalogSHA256
	if len(short) > 12 {
		short = short[:12]
	}
	info.Version = catalog.GeneratedAt.UTC().Format("20060102T150405Z") + "-" + short
	for key, value := range map[string]string{
		"schema": info.Schema, "version": info.Version, "catalog_schema": catalog.Schema,
		"catalog_generated_at": info.CatalogGeneratedAt, "catalog_sha256": info.CatalogSHA256,
		"resolutions": strconv.Itoa(info.Resolutions), "signatures": strconv.Itoa(info.Signatures),
		"vectors": strconv.Itoa(info.Vectors), "dimensions": strconv.Itoa(VectorDimensions),
		"vector_encoding": "float32le", "embedding_model": info.EmbeddingModel,
	} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta VALUES (?, ?)`, key, value); err != nil {
			return SnapshotInfo{}, fmt.Errorf("write snapshot metadata: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO resolutions_fts (resolutions_fts) VALUES ('optimize')`); err != nil {
		return SnapshotInfo{}, fmt.Errorf("optimize snapshot index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SnapshotInfo{}, err
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return SnapshotInfo{}, fmt.Errorf("compact snapshot: %w", err)
	}
	return info, nil
}
