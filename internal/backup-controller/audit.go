package backupcontroller

import (
	"context"
	"log/slog"
)

// AuditLog wraps the Store's AppendAudit so the server doesn't have to
// know about the persistence layer for audit events specifically. It
// also fans out to slog so audit lines show up alongside other
// controller logs in the JSON-Lines stream — useful for forwarding to
// SIEMs that already tail the slog output.
//
// The AuditLog is intentionally not the only path that writes audit
// rows: tests or a future Phase-5 OIDC interceptor may write directly
// to the Store. The slog mirror means a missed write is at least
// visible in the operational log if the Store call fails.
type AuditLog struct {
	Store  Store
	Logger *slog.Logger
}

// Append persists the entry. Returns the Store's error verbatim so
// callers can decide whether a failed audit write is fatal (the
// security-conscious answer is "yes, fail the request").
func (a *AuditLog) Append(ctx context.Context, e *AuditEntry) error {
	if err := a.Store.AppendAudit(ctx, e); err != nil {
		return err
	}
	a.log().Info("audit",
		"tenant", e.TenantID,
		"actor", e.Actor,
		"action", e.Action,
		"resource", e.Resource,
	)
	return nil
}

func (a *AuditLog) log() *slog.Logger {
	if a.Logger == nil {
		return slog.Default()
	}
	return a.Logger
}
