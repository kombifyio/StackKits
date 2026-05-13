package backupcontroller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
)

// AgentEnrollment is the payload returned to the operator when a host
// is enrolled. The Token is the per-host shared secret the agent uses
// to authenticate its calls back to the controller; it is shown
// exactly once (in the kombify-TechStack dashboard) and must be copied
// into the agent's local config via `stackkit backup enroll --token`.
//
// The token is 32 random bytes encoded as URL-safe base64 (~43 chars).
// Same shape as TinyAuthStaticGenerator's plaintext password — high
// entropy, safe for headers, copy-pasteable in onboarding flows.
type AgentEnrollment struct {
	HostID string `json:"host_id"`
	Token  string `json:"token"`
}

// EnrollHost creates a Host row, mints a token, and writes both the
// host and an audit entry. Returns the AgentEnrollment with the
// freshly-minted token.
//
// The Store is responsible for actually persisting; this function just
// orchestrates token generation and audit logging so the server's
// handler can stay thin. A real Phase-4-final controller will store
// the token as a salted hash (the agent presents the original) — this
// scaffold stores it verbatim because the in-memory store has no
// persistence threat model.
func EnrollHost(ctx context.Context, store Store, audit *AuditLog, host *Host, actor string) (*AgentEnrollment, error) {
	token, err := newAgentToken()
	if err != nil {
		return nil, err
	}
	host.AgentToken = token
	if err := store.CreateHost(ctx, host); err != nil {
		return nil, err
	}
	if audit != nil {
		_ = audit.Append(ctx, &AuditEntry{
			Actor:    actor,
			Action:   "host.enroll",
			Resource: "host:" + host.ID,
			Payload: map[string]interface{}{
				"hostname": host.Hostname,
				"fleet":    host.FleetID,
			},
		})
	}
	return &AgentEnrollment{HostID: host.ID, Token: token}, nil
}

func newAgentToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
