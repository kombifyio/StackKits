// Package mcpagents holds the per-agent StackKits MCP credentials and the
// per-call owner approvals of one installation. The server stores only a
// SHA-256 digest of each credential, so the file can be read without
// disclosing a usable token, and every request re-reads it so a revocation or
// approval takes effect immediately and from a separate owner CLI process.
package mcpagents

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StateFile is the workspace-relative location of the store.
const StateFile = ".stackkit/mcp/agents.json"

const (
	tokenPrefix = "skmcp_"
	// ApprovalTTL bounds how long a pending or granted approval stays usable.
	ApprovalTTL = 15 * time.Minute
	// MaxPendingPerAgent caps unanswered requests so an agent cannot flood the owner.
	MaxPendingPerAgent = 20
	maxAgents          = 64
	lockWait           = 5 * time.Second
	lockStale          = 30 * time.Second
)

// Agent is the public record of one credential; it never carries the token.
type Agent struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"createdAt"`
	RotatedAt *time.Time `json:"rotatedAt,omitempty"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// Active reports whether the credential can still authenticate.
func (a Agent) Active() bool { return a.RevokedAt == nil }

// ApprovalState is the owner's answer to one requested mutation.
type ApprovalState string

const (
	ApprovalPending  ApprovalState = "pending"
	ApprovalApproved ApprovalState = "approved"
	ApprovalDenied   ApprovalState = "denied"
	ApprovalUsed     ApprovalState = "used"
)

// Approval binds one owner decision to one exact call of one agent.
type Approval struct {
	ID        string        `json:"id"`
	AgentID   string        `json:"agentId"`
	Operation string        `json:"operation"`
	Digest    string        `json:"digest"`
	Summary   string        `json:"summary,omitempty"`
	State     ApprovalState `json:"state"`
	CreatedAt time.Time     `json:"createdAt"`
	ExpiresAt time.Time     `json:"expiresAt"`
}

type record struct {
	Agent
	TokenSHA256 string `json:"tokenSha256"`
}

type document struct {
	Agents    []record   `json:"agents"`
	Approvals []Approval `json:"approvals"`
}

// Store is the on-disk credential and approval state of one workspace.
type Store struct {
	path string
	now  func() time.Time
}

// Open returns the store of a workspace; the file is created on first write.
func Open(workspace string) *Store {
	return &Store{path: filepath.Join(workspace, filepath.FromSlash(StateFile)), now: time.Now}
}

// Mint creates an agent credential. The token is returned exactly once.
func (s *Store) Mint(name string) (Agent, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "\r\n\x00") {
		return Agent{}, "", errors.New("agent name must be 1 to 64 characters on one line")
	}
	var agent Agent
	var token string
	err := s.update(func(doc *document) error {
		active := 0
		for _, r := range doc.Agents {
			if r.Active() {
				active++
				if strings.EqualFold(r.Name, name) {
					return fmt.Errorf("an active agent credential named %q already exists; rotate or revoke it", name)
				}
			}
		}
		if active >= maxAgents {
			return fmt.Errorf("at most %d active agent credentials; revoke one first", maxAgents)
		}
		id, err := randomText(6)
		if err != nil {
			return err
		}
		token, err = newToken()
		if err != nil {
			return err
		}
		agent = Agent{ID: "agent_" + id, Name: name, CreatedAt: s.now().UTC()}
		doc.Agents = append(doc.Agents, record{Agent: agent, TokenSHA256: digest(token)})
		return nil
	})
	return agent, token, err
}

// Rotate replaces an active credential's token; the old token stops working at once.
func (s *Store) Rotate(id string) (Agent, string, error) {
	var agent Agent
	var token string
	err := s.update(func(doc *document) error {
		r := find(doc, id)
		if r == nil || !r.Active() {
			return fmt.Errorf("no active agent credential %q", id)
		}
		var err error
		if token, err = newToken(); err != nil {
			return err
		}
		now := s.now().UTC()
		r.TokenSHA256, r.RotatedAt = digest(token), &now
		agent = r.Agent
		return nil
	})
	return agent, token, err
}

// Revoke ends a credential and discards its unused approvals.
func (s *Store) Revoke(id string) error {
	return s.update(func(doc *document) error {
		r := find(doc, id)
		if r == nil {
			return fmt.Errorf("no agent credential %q", id)
		}
		if r.RevokedAt == nil {
			now := s.now().UTC()
			r.RevokedAt = &now
		}
		kept := doc.Approvals[:0]
		for _, a := range doc.Approvals {
			if a.AgentID != id || a.State == ApprovalUsed {
				kept = append(kept, a)
			}
		}
		doc.Approvals = kept
		return nil
	})
}

// List returns every credential, newest first, without token material.
func (s *Store) List() ([]Agent, error) {
	doc, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(doc.Agents))
	for _, r := range doc.Agents {
		out = append(out, r.Agent)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// HasActive reports whether any credential can still authenticate.
func (s *Store) HasActive() bool {
	doc, err := s.read()
	if err != nil {
		return false
	}
	for _, r := range doc.Agents {
		if r.Active() {
			return true
		}
	}
	return false
}

// Authenticate resolves a presented token to its active agent. Every stored
// digest is compared in constant time, so timing reveals neither which
// credential matched nor how many exist.
func (s *Store) Authenticate(token string) (Agent, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, tokenPrefix) {
		return Agent{}, false
	}
	doc, err := s.read()
	if err != nil {
		return Agent{}, false
	}
	presented := digest(token)
	var match Agent
	found := 0
	for _, r := range doc.Agents {
		if subtle.ConstantTimeCompare([]byte(r.TokenSHA256), []byte(presented)) == 1 && r.Active() {
			match, found = r.Agent, 1
		}
	}
	return match, found == 1
}

// Request records a pending approval for one exact call, or returns the
// existing pending one, so a retried call does not multiply requests.
func (s *Store) Request(agentID, operation, callDigest, summary string) (Approval, error) {
	var out Approval
	err := s.update(func(doc *document) error {
		r := find(doc, agentID)
		if r == nil || !r.Active() {
			return fmt.Errorf("agent credential %q is not active", agentID)
		}
		now := s.now().UTC()
		pending := 0
		for _, a := range doc.Approvals {
			if a.AgentID != agentID || a.State != ApprovalPending {
				continue
			}
			if a.Operation == operation && a.Digest == callDigest {
				out = a
				return nil
			}
			pending++
		}
		if pending >= MaxPendingPerAgent {
			return fmt.Errorf("agent %q has %d unanswered approval requests; the owner must answer them first", agentID, pending)
		}
		id, err := randomText(6)
		if err != nil {
			return err
		}
		out = Approval{ID: "appr_" + id, AgentID: agentID, Operation: operation, Digest: callDigest,
			Summary: truncate(summary, 200), State: ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(ApprovalTTL)}
		doc.Approvals = append(doc.Approvals, out)
		return nil
	})
	return out, err
}

// Approvals lists unexpired approvals, newest first.
func (s *Store) Approvals() ([]Approval, error) {
	doc, err := s.read()
	if err != nil {
		return nil, err
	}
	now := s.now()
	var out []Approval
	for _, a := range doc.Approvals {
		if a.ExpiresAt.After(now) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Decide records the owner's answer to a pending request.
func (s *Store) Decide(id string, approve bool) (Approval, error) {
	var out Approval
	err := s.update(func(doc *document) error {
		for i := range doc.Approvals {
			a := &doc.Approvals[i]
			if a.ID != id {
				continue
			}
			if a.State != ApprovalPending || !a.ExpiresAt.After(s.now()) {
				return fmt.Errorf("approval %q is no longer pending", id)
			}
			a.State = ApprovalDenied
			if approve {
				a.State = ApprovalApproved
				a.ExpiresAt = s.now().UTC().Add(ApprovalTTL)
			}
			out = *a
			return nil
		}
		return fmt.Errorf("no approval request %q", id)
	})
	return out, err
}

// Consume spends a granted approval for exactly this agent, operation and
// call digest. An approval authorizes one call and is never reusable.
func (s *Store) Consume(agentID, operation, callDigest string) (bool, error) {
	granted := false
	err := s.update(func(doc *document) error {
		r := find(doc, agentID)
		if r == nil || !r.Active() {
			return nil
		}
		for i := range doc.Approvals {
			a := &doc.Approvals[i]
			if a.AgentID == agentID && a.Operation == operation && a.Digest == callDigest &&
				a.State == ApprovalApproved && a.ExpiresAt.After(s.now()) {
				a.State, granted = ApprovalUsed, true
				return nil
			}
		}
		return nil
	})
	return granted, err
}

func find(doc *document, id string) *record {
	for i := range doc.Agents {
		if doc.Agents[i].ID == id {
			return &doc.Agents[i]
		}
	}
	return nil
}

func (s *Store) read() (document, error) {
	var doc document
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("read MCP agent store: %w", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("MCP agent store %s is not valid JSON: %w", s.path, err)
	}
	return doc, nil
}

// update applies fn under an exclusive lock and writes the result atomically.
// Spent and expired approvals are dropped after a day so the file stays small.
func (s *Store) update(fn func(*document) error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("prepare MCP agent store: %w", err)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(&doc); err != nil {
		return err
	}
	cutoff := s.now().Add(-24 * time.Hour)
	kept := doc.Approvals[:0]
	for _, a := range doc.Approvals {
		if a.ExpiresAt.After(cutoff) {
			kept = append(kept, a)
		}
	}
	doc.Approvals = kept
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".agents-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// lock takes a lock file shared by the server and the owner CLI. A lock older
// than lockStale belongs to a crashed process and is taken over.
func (s *Store) lock() (func(), error) {
	path := s.path + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock MCP agent store: %w", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("MCP agent store is locked by another process")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newToken() (string, error) {
	text, err := randomText(32)
	if err != nil {
		return "", err
	}
	return tokenPrefix + text, nil
}

func randomText(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CallDigest binds an approval to the exact operation, workspace and
// arguments, so a granted approval cannot be reused for a different call.
func CallDigest(operation string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(operation))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
