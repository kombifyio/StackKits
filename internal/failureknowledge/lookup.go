package failureknowledge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// OllamaURLEnv names a local Ollama that serves bge-m3; without it the
	// semantic step does not run.
	OllamaURLEnv = "STACKKIT_FAILURE_OLLAMA_URL"
	ollamaModel  = "bge-m3"
	rrfK         = 60
	defaultTopK  = 5
	maxTopK      = 20
	// The query text is cut like the catalog embedder cuts its input.
	maxEmbedBytes = 16 << 10
)

// Query is a normalized failure signature, a free-text description, or both.
type Query struct {
	Signature string
	Text      string
	TopK      int
	// OllamaURL enables the semantic step; empty skips it.
	OllamaURL string
}

// Leg reports how one lookup step ran.
type Leg struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Matches int    `json:"matches"`
	Detail  string `json:"detail,omitempty"`
}

// Resolution is a promoted resolution as the snapshot holds it.
type Resolution struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Symptoms      []Symptom     `json:"symptoms"`
	Applicability Applicability `json:"applicability"`
	RootCause     string        `json:"rootCause"`
	FixSteps      []FixStep     `json:"fixSteps"`
	Evidence      Evidence      `json:"evidence"`
	Revision      int           `json:"revision"`
	ReviewedAt    string        `json:"reviewedAt,omitempty"`
}

// Match is one lookup result.
type Match struct {
	Rank  int            `json:"rank"`
	Match string         `json:"match"`
	Score float64        `json:"score"`
	Ranks map[string]int `json:"ranks"`
	// Similarity is the cosine similarity of the semantic step, when it ran.
	Similarity *float64   `json:"similarity,omitempty"`
	Resolution Resolution `json:"resolution"`
}

// Result is the lookup answer. A resolution is advisory: a fix runs only
// through the referenced product procedure.
type Result struct {
	Snapshot SnapshotInfo `json:"snapshot"`
	Results  []Match      `json:"results"`
	Legs     []Leg        `json:"legs"`
	Advisory bool         `json:"advisory"`
}

// Snapshot is an opened, read-only snapshot.
type Snapshot struct {
	db   *sql.DB
	info SnapshotInfo
}

// DefaultSnapshotPath finds the snapshot: STACKKIT_FAILURE_SNAPSHOT, else
// beside the executable, in its bin directory or in ../lib/stackkit, where
// release archives and installers place it.
func DefaultSnapshotPath() (string, bool) {
	if override := strings.TrimSpace(os.Getenv(SnapshotEnv)); override != "" {
		return override, fileExists(override)
	}
	executable, err := os.Executable()
	if err != nil || executable == "" {
		return "", false
	}
	dir := filepath.Dir(executable)
	for _, candidate := range []string{
		filepath.Join(dir, SnapshotFileName),
		filepath.Join(dir, "bin", SnapshotFileName),
		filepath.Join(dir, "..", "lib", "stackkit", SnapshotFileName),
	} {
		if fileExists(candidate) {
			return filepath.Clean(candidate), true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// OpenSnapshot opens a snapshot read-only and checks its schema.
func OpenSnapshot(ctx context.Context, path string) (*Snapshot, error) {
	if !fileExists(path) {
		return nil, fmt.Errorf("failure snapshot %s does not exist", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := openDB("file:" + filepath.ToSlash(absolute) + "?mode=ro&immutable=1")
	if err != nil {
		return nil, fmt.Errorf("open failure snapshot: %w", err)
	}
	meta := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM meta`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read failure snapshot metadata: %w", err)
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			_ = db.Close()
			return nil, err
		}
		meta[key] = value
	}
	_ = rows.Close()
	if meta["schema"] != SnapshotSchema {
		_ = db.Close()
		return nil, fmt.Errorf("failure snapshot schema is %q, want %q", meta["schema"], SnapshotSchema)
	}
	info := SnapshotInfo{Schema: meta["schema"], Version: meta["version"], CatalogGeneratedAt: meta["catalog_generated_at"],
		CatalogSHA256: meta["catalog_sha256"], EmbeddingModel: meta["embedding_model"], Path: absolute}
	_, _ = fmt.Sscan(meta["resolutions"], &info.Resolutions)
	_, _ = fmt.Sscan(meta["signatures"], &info.Signatures)
	_, _ = fmt.Sscan(meta["vectors"], &info.Vectors)
	return &Snapshot{db: db, info: info}, nil
}

// Info describes the opened snapshot.
func (s *Snapshot) Info() SnapshotInfo { return s.info }

// Close releases the snapshot.
func (s *Snapshot) Close() error { return s.db.Close() }

type candidate struct {
	id         string
	signature  bool
	ranks      map[string]int
	rrf        float64
	similarity *float64
}

// Lookup runs the offline lookup: exact signature, then FTS5 lexical and,
// with a local bge-m3, semantic search, fused by reciprocal rank. Signature
// matches rank first.
func (s *Snapshot) Lookup(ctx context.Context, query Query) (Result, error) {
	query.Signature = strings.ToLower(strings.TrimSpace(query.Signature))
	query.Text = strings.TrimSpace(query.Text)
	if query.Signature == "" && query.Text == "" {
		return Result{}, errors.New("a signature or a description is required")
	}
	if query.Signature != "" && !signaturePattern.MatchString(query.Signature) {
		return Result{}, errors.New("a signature is 64 lowercase hex characters")
	}
	if query.TopK <= 0 {
		query.TopK = defaultTopK
	}
	if query.TopK > maxTopK {
		query.TopK = maxTopK
	}
	result := Result{Snapshot: s.info, Results: []Match{}, Advisory: true}
	candidates := map[string]*candidate{}
	add := func(leg, id string, rank int) {
		item := candidates[id]
		if item == nil {
			item = &candidate{id: id, ranks: map[string]int{}}
			candidates[id] = item
		}
		item.ranks[leg] = rank
		if leg == "signature" {
			item.signature = true
			return
		}
		item.rrf += 1 / float64(rrfK+rank)
	}

	if query.Signature == "" {
		result.Legs = append(result.Legs, Leg{Name: "signature", State: "skipped"})
	} else {
		ids, err := s.queryIDs(ctx, `SELECT s.resolution_id FROM resolution_signatures s JOIN resolutions r ON r.id = s.resolution_id
WHERE s.signature = ? ORDER BY r.success_rate DESC, r.id`, query.Signature)
		if err != nil {
			return Result{}, fmt.Errorf("signature lookup: %w", err)
		}
		for index, id := range ids {
			add("signature", id, index+1)
		}
		result.Legs = append(result.Legs, Leg{Name: "signature", State: "ok", Matches: len(ids)})
	}

	if match := ftsQuery(query.Text); match == "" {
		result.Legs = append(result.Legs, Leg{Name: "lexical", State: "skipped"})
	} else {
		ids, err := s.queryIDs(ctx, `SELECT resolution_id FROM resolutions_fts WHERE resolutions_fts MATCH ?
ORDER BY bm25(resolutions_fts, 0, 2.0, 1.0), resolution_id LIMIT 20`, match)
		if err != nil {
			return Result{}, fmt.Errorf("lexical lookup: %w", err)
		}
		for index, id := range ids {
			add("lexical", id, index+1)
		}
		result.Legs = append(result.Legs, Leg{Name: "lexical", State: "ok", Matches: len(ids)})
	}

	result.Legs = append(result.Legs, s.semantic(ctx, query, add, candidates))

	ordered := make([]*candidate, 0, len(candidates))
	for _, item := range candidates {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.signature != b.signature {
			return a.signature
		}
		if a.signature && a.ranks["signature"] != b.ranks["signature"] {
			return a.ranks["signature"] < b.ranks["signature"]
		}
		if a.rrf != b.rrf {
			return a.rrf > b.rrf
		}
		return a.id < b.id
	})
	if len(ordered) > query.TopK {
		ordered = ordered[:query.TopK]
	}
	for index, item := range ordered {
		resolution, err := s.resolution(ctx, item.id)
		if err != nil {
			return Result{}, err
		}
		match := "lexical_semantic"
		switch {
		case item.signature:
			match = "exact_signature"
		case item.ranks["semantic"] == 0:
			match = "lexical"
		case item.ranks["lexical"] == 0:
			match = "semantic"
		}
		score := item.rrf
		if item.signature {
			score = 1
		}
		result.Results = append(result.Results, Match{Rank: index + 1, Match: match, Score: math.Round(score*10000) / 10000,
			Ranks: item.ranks, Similarity: item.similarity, Resolution: resolution})
	}
	return result, nil
}

func (s *Snapshot) queryIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Snapshot) resolution(ctx context.Context, id string) (Resolution, error) {
	var out Resolution
	var symptoms, applicability, fixSteps string
	err := s.db.QueryRowContext(ctx, `SELECT id, title, root_cause, symptoms_json, applicability_json, fix_steps_json,
success_count, failure_count, success_rate, revision, reviewed_at FROM resolutions WHERE id = ?`, id).Scan(
		&out.ID, &out.Title, &out.RootCause, &symptoms, &applicability, &fixSteps,
		&out.Evidence.SuccessCount, &out.Evidence.FailureCount, &out.Evidence.SuccessRate, &out.Revision, &out.ReviewedAt)
	if err != nil {
		return out, fmt.Errorf("read resolution %s: %w", id, err)
	}
	for _, part := range []struct {
		raw    string
		target any
	}{{symptoms, &out.Symptoms}, {applicability, &out.Applicability}, {fixSteps, &out.FixSteps}} {
		if err := json.Unmarshal([]byte(part.raw), part.target); err != nil {
			return out, fmt.Errorf("read resolution %s: %w", id, err)
		}
	}
	return out, nil
}

// stopwords are dropped from a lexical query, as the hosted search drops them
// through its english and german text configurations.
var stopwords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`a an and are as at be because but by for from has have i if in into is it its my no not of on or so that the their then there this to was when which will with ` +
		`aber als am an auch auf aus bei bin bis das dass dem den der des die du ein eine einem einen einer er es fur für hat ich im in ist mein meine mit nach nicht noch nur oder sich sie sind und vom von war wenn wie wir zu zum zur`) {
		stopwords[word] = true
	}
}

// ftsQuery turns free text into an FTS5 query: every word of at least two
// characters that is not a stopword, quoted, joined by OR, so no input is FTS5
// syntax.
func ftsQuery(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	seen := map[string]bool{}
	var terms []string
	for _, word := range words {
		if len([]rune(word)) < 2 || stopwords[word] || seen[word] || len(terms) == 32 {
			continue
		}
		seen[word] = true
		terms = append(terms, `"`+word+`"`)
	}
	return strings.Join(terms, " OR ")
}

// semantic embeds the description with the local bge-m3 and ranks the
// snapshot's vectors by cosine similarity.
func (s *Snapshot) semantic(ctx context.Context, query Query, add func(leg, id string, rank int), candidates map[string]*candidate) Leg {
	leg := Leg{Name: "semantic", State: "skipped"}
	if query.Text == "" {
		return leg
	}
	if strings.TrimSpace(query.OllamaURL) == "" {
		leg.Detail = "set " + OllamaURLEnv + " or --ollama-url to a local Ollama with " + ollamaModel
		return leg
	}
	if s.info.Vectors == 0 {
		leg.State, leg.Detail = "unavailable", "the snapshot carries no vectors"
		return leg
	}
	vector, err := embed(ctx, query.OllamaURL, query.Text)
	if err != nil {
		leg.State, leg.Detail = "unavailable", err.Error()
		return leg
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, embedding FROM resolutions WHERE embedding IS NOT NULL`)
	if err != nil {
		leg.State, leg.Detail = "failed", err.Error()
		return leg
	}
	defer rows.Close()
	type scored struct {
		id         string
		similarity float64
	}
	var scores []scored
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil || len(raw) != 4*VectorDimensions {
			continue
		}
		scores = append(scores, scored{id: id, similarity: cosine(vector, raw)})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].similarity != scores[j].similarity {
			return scores[i].similarity > scores[j].similarity
		}
		return scores[i].id < scores[j].id
	})
	if len(scores) > 20 {
		scores = scores[:20]
	}
	for index, item := range scores {
		add("semantic", item.id, index+1)
		similarity := math.Round(item.similarity*10000) / 10000
		candidates[item.id].similarity = &similarity
	}
	leg.State, leg.Matches = "ok", len(scores)
	return leg
}

func cosine(query []float64, raw []byte) float64 {
	var dot, a, b float64
	for index := range query {
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4*index:])))
		dot += query[index] * value
		a += query[index] * query[index]
		b += value * value
	}
	if a == 0 || b == 0 {
		return 0
	}
	return dot / (math.Sqrt(a) * math.Sqrt(b))
}

// requireLoopback keeps the semantic step on this host: the Ollama URL must
// name localhost or a loopback address.
func requireLoopback(baseURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("the Ollama URL %q is not an http(s) URL", baseURL)
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("the Ollama URL must name this host (localhost or a loopback address), not %s", host)
}

func embed(ctx context.Context, baseURL, text string) ([]float64, error) {
	if len(text) > maxEmbedBytes {
		cut := maxEmbedBytes
		for cut > 0 && (text[cut]&0xC0) == 0x80 {
			cut--
		}
		text = text[:cut]
	}
	if err := requireLoopback(baseURL); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"model": ollamaModel, "input": []string{text}})
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	// The description may carry host names or secrets from an error line: it
	// goes to the configured local Ollama only, never through a redirect.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("local embedding failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("local embedding failed: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local embedding answered HTTP %d", response.StatusCode)
	}
	var decoded struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("local embedding answer is not JSON: %w", err)
	}
	if len(decoded.Embeddings) != 1 || len(decoded.Embeddings[0]) != VectorDimensions {
		return nil, fmt.Errorf("local embedding is not a %d-dimension %s vector", VectorDimensions, ollamaModel)
	}
	return decoded.Embeddings[0], nil
}
