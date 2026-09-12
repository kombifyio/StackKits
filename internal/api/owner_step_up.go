package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localowner"
)

const ownerStepUpPath = "/api/v1/identity/step-up"

type ownerStepUpFlow struct {
	binding          localowner.RemoteActionApprovalBinding
	issuer, verifier string
	browserHash      [32]byte
	started          bool
}

type ownerStepUpHandler struct {
	root, origin string
	mu           sync.Mutex
	flows        map[string]ownerStepUpFlow
}

// Browser routes carry a random, short-lived flow capability and a separate
// secure browser cookie. They expose no lifecycle operation and never treat the
// API key, LAN, owner custody, or proxy identity headers as human authentication.
func (s *Server) registerOwnerStepUpRoutes() {
	if s.config.OwnerStepUpOrigin == "" || s.config.APIKey == "" {
		return
	}
	origin, err := localowner.NormalizeStepUpOrigin(s.config.OwnerStepUpOrigin)
	if err != nil {
		slog.Error("Owner step-up disabled: configure an HTTPS origin without a path, query or fragment", "setting", "STACKKIT_OWNER_STEP_UP_ORIGIN")
		return
	}
	h := &ownerStepUpHandler{root: s.config.BaseDir, origin: origin, flows: make(map[string]ownerStepUpFlow)}
	s.mux.HandleFunc("POST "+ownerStepUpPath, h.create)
	s.mux.HandleFunc("GET "+ownerStepUpPath+"/review", h.review)
	s.mux.HandleFunc("POST "+ownerStepUpPath+"/review", h.confirm)
	s.mux.HandleFunc("GET "+localowner.StepUpCallbackPath, h.callback)
}

func isOwnerStepUpBrowserRoute(r *http.Request) bool {
	return (r.URL.Path == ownerStepUpPath+"/review" && (r.Method == http.MethodGet || r.Method == http.MethodPost)) ||
		(r.URL.Path == localowner.StepUpCallbackPath && r.Method == http.MethodGet)
}

func stepUpHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}

func stepUpFailure(w http.ResponseWriter) {
	http.Error(w, "Owner approval unavailable or expired. Request a new approval for the current plan.", http.StatusForbidden)
}

func (h *ownerStepUpHandler) create(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	var binding localowner.RemoteActionApprovalBinding
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || decoder.Decode(new(any)) != io.EOF {
		stepUpFailure(w)
		return
	}
	if _, err := localowner.StepUpNonce(binding); err != nil || time.Now().Before(binding.IssuedAt) || !time.Now().Before(binding.ExpiresAt) {
		stepUpFailure(w)
		return
	}
	service, err := localowner.NewService(h.root)
	if err != nil {
		stepUpFailure(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	trust, err := service.PrepareStepUp(ctx, h.origin)
	if err != nil || trust.OwnerRef != binding.OwnerRef || trust.HomeSiteRef != binding.HomeSiteRef {
		stepUpFailure(w)
		return
	}
	state, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	verifier, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	h.mu.Lock()
	for id, flow := range h.flows {
		if !time.Now().Before(flow.binding.ExpiresAt) {
			delete(h.flows, id)
		}
	}
	if len(h.flows) >= 128 {
		h.mu.Unlock()
		stepUpFailure(w)
		return
	}
	h.flows[state] = ownerStepUpFlow{binding: binding, issuer: trust.Issuer, verifier: verifier}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"approvalUrl": h.origin + ownerStepUpPath + "/review?state=" + url.QueryEscape(state), "expiresAt": binding.ExpiresAt})
}

var ownerStepUpReview = template.Must(template.New("owner-step-up").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Approve StackKits action</title><h1>Approve {{.Binding.Action}}</h1><p>Owner: {{.Binding.OwnerRef}}</p><p>Target: {{.Binding.TargetSiteRef}} / {{.Binding.TargetNodeRef}}</p><p>Plan: <code>{{.Binding.PlanHash}}</code></p><p>Action: <code>{{.Binding.ActionDigest}}</code></p><p>Approval expires at {{.Binding.ExpiresAt}}. Continue to your local PocketID to authenticate with your passkey. This page does not execute the action.</p><form method="post"><input type="hidden" name="state" value="{{.State}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Approve this action with PocketID</button></form></html>`))

func (h *ownerStepUpHandler) review(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	state := r.URL.Query().Get("state")
	h.mu.Lock()
	flow, ok := h.flows[state]
	h.mu.Unlock()
	if !ok || flow.started || !time.Now().Before(flow.binding.ExpiresAt) {
		stepUpFailure(w)
		return
	}
	if !stepUpFormPolicy(w, flow.issuer) {
		stepUpFailure(w)
		return
	}
	browser, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	// Same-origin form POSTs need a non-null Origin. Cross-origin navigation
	// to PocketID must still omit the review URL and its flow capability.
	w.Header().Set("Referrer-Policy", "same-origin")
	http.SetCookie(w, &http.Cookie{Name: "__Host-stackkit-step-up", Value: browser, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: flow.binding.ExpiresAt})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ownerStepUpReview.Execute(w, struct {
		Binding     localowner.RemoteActionApprovalBinding
		State, CSRF string
	}{flow.binding, state, stepUpCSRF(browser, state)})
}

func (h *ownerStepUpHandler) confirm(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.Header.Get("Origin") != h.origin || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") || r.ParseForm() != nil {
		stepUpFailure(w)
		return
	}
	state := r.PostForm.Get("state")
	cookie, err := r.Cookie("__Host-stackkit-step-up")
	h.mu.Lock()
	flow, ok := h.flows[state]
	if err != nil || !ok || flow.started || !time.Now().Before(flow.binding.ExpiresAt) || len(cookie.Value) != 43 || r.PostForm.Get("csrf") != stepUpCSRF(cookie.Value, state) {
		h.mu.Unlock()
		stepUpFailure(w)
		return
	}
	flow.started = true
	flow.browserHash = sha256.Sum256([]byte(cookie.Value))
	h.flows[state] = flow
	h.mu.Unlock()
	if !stepUpFormPolicy(w, flow.issuer) {
		stepUpFailure(w)
		return
	}
	nonce, _ := localowner.StepUpNonce(flow.binding)
	challenge := sha256.Sum256([]byte(flow.verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {localowner.StepUpClientID}, "redirect_uri": {h.origin + localowner.StepUpCallbackPath}, "scope": {"openid"}, "state": {state}, "nonce": {nonce}, "prompt": {"login"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	http.Redirect(w, r, flow.issuer+"/authorize?"+query.Encode(), http.StatusSeeOther)
}

func (h *ownerStepUpHandler) callback(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("__Host-stackkit-step-up")
	h.mu.Lock()
	flow, ok := h.flows[state]
	browserMatches := err == nil && sha256.Sum256([]byte(cookie.Value)) == flow.browserHash
	if ok && flow.started && !browserMatches {
		// A forwarded authorization must never become redeemable later by the
		// original caller after the owner's browser rejects its callback.
		delete(h.flows, state)
	}
	if !ok || !flow.started || !browserMatches || !time.Now().Before(flow.binding.ExpiresAt) || r.URL.Query().Get("iss") != flow.issuer {
		h.mu.Unlock()
		stepUpFailure(w)
		return
	}
	delete(h.flows, state)
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "__Host-stackkit-step-up", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	service, err := localowner.NewService(h.root)
	if err != nil {
		stepUpFailure(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	receipt, err := service.ExchangeStepUpCode(ctx, h.origin, r.URL.Query().Get("code"), flow.verifier, flow.binding)
	if err != nil {
		stepUpFailure(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="stackkit-owner-approval.json"`)
	_, _ = w.Write(receipt)
}

func stepUpFormPolicy(w http.ResponseWriter, issuer string) bool {
	origin, err := localowner.NormalizeStepUpOrigin(issuer)
	if err != nil {
		return false
	}
	// Chromium checks a form's redirected destination as well as its initial
	// target. Only this Home owner's verified local issuer is permitted.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' "+origin+"; frame-ancestors 'none'; base-uri 'none'")
	return true
}

func stepUpCSRF(browser, state string) string {
	digest := sha256.Sum256([]byte(browser + "\x00" + state))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func stepUpRandom() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
