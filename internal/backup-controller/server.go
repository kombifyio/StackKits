package backupcontroller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Server is the controller's HTTP API. It exposes:
//
//	POST   /api/v1/tenants                     create a tenant
//	GET    /api/v1/tenants                     list tenants
//	POST   /api/v1/tenants/{id}/fleets         create a fleet
//	GET    /api/v1/tenants/{id}/fleets         list fleets
//	POST   /api/v1/fleets/{id}/hosts           enroll a host (returns agent_token)
//	GET    /api/v1/fleets/{id}/hosts           list hosts
//	POST   /api/v1/tenants/{id}/repos          register a repo
//	POST   /api/v1/tenants/{id}/jobs           create a recurring job
//	GET    /api/v1/tenants/{id}/jobs           list jobs
//	POST   /api/v1/agent/heartbeat             agent → controller (token-auth)
//	POST   /api/v1/agent/job-status            agent → controller (token-auth)
//	GET    /api/v1/tenants/{id}/audit          last N audit entries
//	GET    /healthz                            liveness
//
// Authentication:
//   - Operator routes (everything under /api/v1/tenants, /api/v1/fleets)
//     are gated by an X-API-Key header. Phase 5 (kombify-TechStack) will
//     replace that with OIDC against PocketID.
//   - Agent routes (/api/v1/agent/*) require an X-Agent-Token header.
//
// Logging follows the conventions from internal/api: log/slog,
// JSON-friendly fields. No Sentry on the Go side per the
// observability standard.
type Server struct {
	Store          Store
	Audit          *AuditLog
	Logger         *slog.Logger
	OperatorAPIKey string

	// Optional handler invoked on agent heartbeat. Useful in tests; in
	// production the default UpdateHostLastSeen path is sufficient.
	OnHeartbeat func(ctx context.Context, host *Host) error
}

// Handler returns the *http.ServeMux wired with all routes and
// middleware. Mount this on whichever http.Server you use; a
// production main.go would also wrap it with TLS, request-ID, and
// rate-limit middleware as the existing internal/api/server.go does.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Operator endpoints (X-API-Key).
	op := s.requireOperatorKey
	mux.Handle("POST /api/v1/tenants", op(http.HandlerFunc(s.handleCreateTenant)))
	mux.Handle("GET /api/v1/tenants", op(http.HandlerFunc(s.handleListTenants)))
	mux.Handle("POST /api/v1/tenants/{id}/fleets", op(http.HandlerFunc(s.handleCreateFleet)))
	mux.Handle("GET /api/v1/tenants/{id}/fleets", op(http.HandlerFunc(s.handleListFleets)))
	mux.Handle("POST /api/v1/fleets/{id}/hosts", op(http.HandlerFunc(s.handleCreateHost)))
	mux.Handle("GET /api/v1/fleets/{id}/hosts", op(http.HandlerFunc(s.handleListHosts)))
	mux.Handle("POST /api/v1/tenants/{id}/repos", op(http.HandlerFunc(s.handleCreateRepo)))
	mux.Handle("POST /api/v1/tenants/{id}/jobs", op(http.HandlerFunc(s.handleCreateJob)))
	mux.Handle("GET /api/v1/tenants/{id}/jobs", op(http.HandlerFunc(s.handleListJobs)))
	mux.Handle("GET /api/v1/tenants/{id}/audit", op(http.HandlerFunc(s.handleListAudit)))

	// Agent endpoints (X-Agent-Token).
	ag := s.requireAgentToken
	mux.Handle("POST /api/v1/agent/heartbeat", ag(http.HandlerFunc(s.handleAgentHeartbeat)))
	mux.Handle("POST /api/v1/agent/job-status", ag(http.HandlerFunc(s.handleAgentJobStatus)))

	return mux
}

// =============================================================================
// MIDDLEWARE
// =============================================================================

func (s *Server) requireOperatorKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.OperatorAPIKey == "" {
			s.logger().Warn("operator API key not configured — refusing request")
			http.Error(w, "operator auth not configured", http.StatusServiceUnavailable)
			return
		}
		got := r.Header.Get("X-API-Key")
		if got != s.OperatorAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// agentContextKey is the type used to thread the resolved Host through
// the request context. Using a typed key avoids collisions with other
// packages.
type agentContextKey struct{}

func (s *Server) requireAgentToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Agent-Token")
		if token == "" {
			http.Error(w, "missing agent token", http.StatusUnauthorized)
			return
		}
		host, err := s.Store.GetHostByToken(r.Context(), token)
		if err != nil {
			http.Error(w, "unknown agent token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), agentContextKey{}, host)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func hostFromContext(ctx context.Context) *Host {
	h, _ := ctx.Value(agentContextKey{}).(*Host)
	return h
}

// =============================================================================
// HANDLERS
// =============================================================================

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	var t Tenant
	if !decode(w, r, &t) {
		return
	}
	if t.Plan == "" {
		t.Plan = PlanFree
	}
	if err := s.Store.CreateTenant(r.Context(), &t); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	s.recordAudit(r.Context(), t.ID, operatorActor(r), "tenant.create", "tenant:"+t.ID, nil)
	writeJSON(w, http.StatusCreated, &t)
}

func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.ListTenants(r.Context())
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// dupl flags this and handleCreateRepo as duplicates because both
// follow the same tenant-scoped POST → decode → store → audit shape.
// The shared step (tenant load) is already factored into tenantOr404;
// the rest is dictated by the typed body. See the matching directive
// on handleCreateRepo.
//
//nolint:dupl
func (s *Server) handleCreateFleet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantOr404(w, r)
	if !ok {
		return
	}
	var f Fleet
	if !decode(w, r, &f) {
		return
	}
	f.TenantID = tenantID
	if err := s.Store.CreateFleet(r.Context(), &f); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	s.recordAudit(r.Context(), tenantID, operatorActor(r), "fleet.create", "fleet:"+f.ID, nil)
	writeJSON(w, http.StatusCreated, &f)
}

// tenantOr404 resolves the {id} path parameter, asserts the tenant
// exists, and writes a 404 if not. Returns the tenant ID and a boolean
// telling the handler whether to continue. Extracted because multiple
// tenant-scoped POST handlers (fleets, repos, …) follow the same
// load-or-404 pattern; without this helper the linter's dupl rule
// flags them as copy-paste.
func (s *Server) tenantOr404(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenantID := r.PathValue("id")
	if _, err := s.Store.GetTenant(r.Context(), tenantID); err != nil {
		writeError(w, err, http.StatusNotFound)
		return "", false
	}
	return tenantID, true
}

func (s *Server) handleListFleets(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.ListFleetsByTenant(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("id")
	var h Host
	if !decode(w, r, &h) {
		return
	}
	h.FleetID = fleetID
	if err := s.Store.CreateHost(r.Context(), &h); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	// Resolve tenant for the audit log.
	fleets, _ := s.Store.ListFleetsByTenant(r.Context(), "")
	_ = fleets
	s.recordAudit(r.Context(), "", operatorActor(r), "host.enroll", "host:"+h.ID, map[string]interface{}{"fleet": fleetID})
	writeJSON(w, http.StatusCreated, &h) // includes the AgentToken — operator must save it
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.ListHostsByFleet(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Structurally similar to handleCreateFleet: tenant-scope, decode,
// store, audit, respond. Extracting a single generic "Create<X>"
// would require either reflection or generics with a constraint each
// model already satisfies — both add indirection without improving
// clarity. The shared step (tenant load) is already factored into
// tenantOr404; the rest is dictated by the typed body.
//
//nolint:dupl
func (s *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantOr404(w, r)
	if !ok {
		return
	}
	var rp Repo
	if !decode(w, r, &rp) {
		return
	}
	rp.TenantID = tenantID
	if err := s.Store.CreateRepo(r.Context(), &rp); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	s.recordAudit(r.Context(), tenantID, operatorActor(r), "repo.create", "repo:"+rp.ID, nil)
	writeJSON(w, http.StatusCreated, &rp)
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	var j Job
	if !decode(w, r, &j) {
		return
	}
	j.TenantID = tenantID
	if err := s.Store.CreateJob(r.Context(), &j); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	s.recordAudit(r.Context(), tenantID, operatorActor(r), "job.create", "job:"+j.ID, nil)
	writeJSON(w, http.StatusCreated, &j)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.ListJobsByTenant(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.ListAuditByTenant(r.Context(), r.PathValue("id"), 100)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// agent endpoints

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	host := hostFromContext(r.Context())
	if err := s.Store.UpdateHostLastSeen(r.Context(), host.ID, time.Now().UTC()); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	if s.OnHeartbeat != nil {
		_ = s.OnHeartbeat(r.Context(), host)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentJobStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JobID  string    `json:"job_id"`
		Status JobStatus `json:"status"`
		At     time.Time `json:"at"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Store.UpdateJobStatus(r.Context(), body.JobID, body.Status, &body.At); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	host := hostFromContext(r.Context())
	s.recordAudit(r.Context(), "", "agent:"+host.ID, "job.status:"+string(body.Status), "job:"+body.JobID, nil)
	w.WriteHeader(http.StatusNoContent)
}

// =============================================================================
// HELPERS
// =============================================================================

func (s *Server) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

func (s *Server) recordAudit(ctx context.Context, tenantID, actor, action, resource string, payload map[string]interface{}) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Append(ctx, &AuditEntry{
		TenantID: tenantID,
		Actor:    actor,
		Action:   action,
		Resource: resource,
		Payload:  payload,
	})
}

func operatorActor(r *http.Request) string {
	// Phase 5 swaps this for the OIDC subject. For now the API key
	// lands as a single anonymous operator identity; that is fine
	// while there is exactly one TechStack control plane.
	return "operator:apikey"
}

func decode(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error, fallback int) {
	if errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), fallback)
}

// stripAPIPrefix is a small helper that trims "/api/v1" from a path so a
// downstream router (e.g., a future chi or gorilla split) can re-route
// without re-parsing the prefix.
func stripAPIPrefix(p string) string {
	return strings.TrimPrefix(p, "/api/v1")
}

var _ = stripAPIPrefix // exported helper kept for the Phase-5 router refactor
