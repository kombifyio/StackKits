package contentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Paths of the node bridge (standard section 4).
const (
	SummaryPath = "/content/v1/summary"
	HealthPath  = "/healthz"
)

const (
	maxInFlight     = 8
	requestDeadline = 10 * time.Second
)

var homelabIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// TierError refuses a request before anything is read from an app.
type TierError struct {
	HTTPStatus int
	Code       string
}

func (e *TierError) Error() string { return "content bridge: " + e.Code }

// TierAuthority decides which tier a request is granted. The bridge never
// reads a tier from anywhere else, and a request without a grant is refused
// before any app is called (consent default off).
//
// The grant belongs to the Gateway: it travels as a signed `tier` claim of
// the installation envelope; the authority that verifies that envelope
// implements this interface.
type TierAuthority interface {
	GrantedTier(r *http.Request) (Tier, error)
}

// LoopbackQueryTier is the authority used until envelope verification
// exists. It accepts the `tier` query parameter only from a caller on the
// loopback interface of the bridge's own network namespace, which is the
// owner on the node (or a test). Behind the router every request comes from
// the router's address, so the public route answers 403 until the envelope
// authority replaces this one. It never reads a forwarded-for header: a
// remote client controls those.
type LoopbackQueryTier struct{}

// GrantedTier implements TierAuthority.
func (LoopbackQueryTier) GrantedTier(r *http.Request) (Tier, error) {
	if !isLoopbackPeer(r.RemoteAddr) {
		return "", &TierError{HTTPStatus: http.StatusForbidden, Code: "tier_not_granted"}
	}
	raw := r.URL.Query()["tier"]
	if len(raw) == 0 {
		return "", &TierError{HTTPStatus: http.StatusBadRequest, Code: "tier_required"}
	}
	tier, ok := ParseTier(raw[0])
	if len(raw) != 1 || !ok {
		return "", &TierError{HTTPStatus: http.StatusBadRequest, Code: "tier_invalid"}
	}
	return tier, nil
}

func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewHandler serves the summary and the loopback health endpoint.
func NewHandler(service *Service, authority TierAuthority) http.Handler {
	h := &handler{service: service, authority: authority, slots: make(chan struct{}, maxInFlight)}
	mux := http.NewServeMux()
	mux.HandleFunc(SummaryPath, h.summary)
	mux.HandleFunc(HealthPath, h.health)
	return mux
}

type handler struct {
	service   *Service
	authority TierAuthority
	slots     chan struct{}
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !isLoopbackPeer(r.RemoteAddr) {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) summary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	tier, err := h.authority.GrantedTier(r)
	if err != nil {
		var refusal *TierError
		if errors.As(err, &refusal) {
			writeError(w, refusal.HTTPStatus, refusal.Code)
		} else {
			writeError(w, http.StatusForbidden, "tier_not_granted")
		}
		return
	}
	useCases, homelabID, code := h.parse(r)
	if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
	defer cancel()
	writeJSON(w, http.StatusOK, h.service.Summarize(ctx, useCases, tier, homelabID))
}

// parse reads the use cases and the optional installation identifier. An
// unknown use case is refused: the contract cannot name it.
func (h *handler) parse(r *http.Request) ([]UseCase, string, string) {
	query := r.URL.Query()
	homelabID := h.service.HomelabID()
	if values := query["homelab_id"]; len(values) > 0 {
		if len(values) != 1 || !homelabIDPattern.MatchString(values[0]) {
			return nil, "", "homelab_id_invalid"
		}
		homelabID = values[0]
	}
	if len(query["use_cases"]) != 1 || strings.TrimSpace(query.Get("use_cases")) == "" {
		return nil, "", "use_cases_required"
	}
	var useCases []UseCase
	seen := map[UseCase]bool{}
	for _, name := range strings.Split(query.Get("use_cases"), ",") {
		useCase, ok := ParseUseCase(strings.TrimSpace(name))
		if !ok {
			return nil, "", "use_case_unknown"
		}
		if !seen[useCase] {
			seen[useCase] = true
			useCases = append(useCases, useCase)
		}
	}
	return useCases, homelabID, ""
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	header := w.Header()
	header.Set("Content-Type", "application/json")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
