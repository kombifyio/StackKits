package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/mcpagents"
)

// The agent-approval mode of the Owner step-up page answers one per-call write
// approval request of a StackKits MCP agent that authenticated with its own
// credential. The MCP denial names the request and links here; the agent never
// receives a way to answer it. The Owner sees the agent, operation and call,
// chooses approve or deny, and authenticates with a passkey at the local
// PocketID through the existing confidential step-up client. Only after
// identity.VerifyOwnerStepUp accepts the ID token for this flow's nonce is the
// answer recorded, and an approval still covers exactly that one call once.
//
// Like device enrollment, opening the page changes no identity resource: it
// needs the already prepared step-up client, and starting a flow needs a
// same-origin POST bound to this browser's cookie.
const (
	ownerStepUpAgentApprovalPath  = ownerStepUpPath + "/agent-approval"
	ownerStepUpAgentApprovalTTL   = 5 * time.Minute
	ownerStepUpAgentApprovalFlows = 16
)

func agentApprovalCSRF(browser, approvalID string) string {
	return stepUpCSRF(browser, "agent-approval\x00"+approvalID)
}

var ownerStepUpAgentApprovalReview = template.Must(template.New("agent-approval-review").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Answer an agent write request</title><h1>Answer an agent write request</h1><p>Agent: <strong>{{.Agent.Name}}</strong> (<code>{{.Agent.ID}}</code>)</p><p>Operation: <code>{{.Approval.Operation}}</code></p><p>Call: <code>{{.Approval.Summary}}</code></p><p>Call digest: <code>{{.Approval.Digest}}</code></p><p>Request expires at {{.Approval.ExpiresAt.Format "15:04:05 MST"}}. Approving lets this agent run this exact call once; any other call needs a new approval. Continue to your local PocketID and authenticate with your Owner passkey to record your answer. This page does not run the call.</p><form method="post"><input type="hidden" name="id" value="{{.Approval.ID}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" name="decision" value="approve">Approve this call with PocketID</button> <button type="submit" name="decision" value="deny">Deny this call with PocketID</button></form></html>`))

var ownerStepUpAgentApprovalDone = template.Must(template.New("agent-approval-done").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Agent write request answered</title><h1>{{if eq .State "approved"}}Call approved{{else}}Call denied{{end}}</h1><p>Request <code>{{.ID}}</code> of agent <code>{{.AgentID}}</code> for <code>{{.Operation}}</code> is {{.State}}.</p>{{if eq .State "approved"}}<p>The agent can repeat the identical call once until {{.ExpiresAt.Format "15:04:05 MST"}}.</p>{{end}}<p>You can close this page.</p></html>`))

func (h *ownerStepUpHandler) reviewAgentApproval(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	approval, agent, err := mcpagents.Open(h.root).Pending(r.URL.Query().Get("id"))
	if err != nil {
		stepUpFailure(w)
		return
	}
	issuer, ok := h.preparedStepUpIssuer(r.Context())
	if !ok || !stepUpFormPolicy(w, issuer) {
		stepUpFailure(w)
		return
	}
	browser, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	w.Header().Set("Referrer-Policy", "same-origin")
	http.SetCookie(w, &http.Cookie{Name: "__Host-stackkit-step-up", Value: browser, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(ownerStepUpAgentApprovalTTL)})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ownerStepUpAgentApprovalReview.Execute(w, struct {
		Approval mcpagents.Approval
		Agent    mcpagents.Agent
		CSRF     string
	}{approval, agent, agentApprovalCSRF(browser, approval.ID)})
}

func (h *ownerStepUpHandler) startAgentApproval(w http.ResponseWriter, r *http.Request) {
	stepUpHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	cookie, err := r.Cookie("__Host-stackkit-step-up")
	if r.Header.Get("Origin") != h.origin || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") || r.ParseForm() != nil ||
		err != nil || len(cookie.Value) != 43 {
		stepUpFailure(w)
		return
	}
	id, decision := r.PostForm.Get("id"), r.PostForm.Get("decision")
	if r.PostForm.Get("csrf") != agentApprovalCSRF(cookie.Value, id) || (decision != "approve" && decision != "deny") {
		stepUpFailure(w)
		return
	}
	approval, _, err := mcpagents.Open(h.root).Pending(id)
	if err != nil {
		stepUpFailure(w)
		return
	}
	issuer, ok := h.preparedStepUpIssuer(r.Context())
	if !ok || !stepUpFormPolicy(w, issuer) {
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
	nonce, err := stepUpRandom()
	if err != nil {
		stepUpFailure(w)
		return
	}
	now := time.Now()
	expires := now.Add(ownerStepUpAgentApprovalTTL)
	if approval.ExpiresAt.Before(expires) {
		expires = approval.ExpiresAt
	}
	flow := ownerStepUpFlow{issuer: issuer, verifier: verifier, started: true, browserHash: sha256.Sum256([]byte(cookie.Value)), nonce: nonce, agentApproval: approval.ID, approve: decision == "approve"}
	flow.binding.IssuedAt, flow.binding.ExpiresAt = now, expires
	h.mu.Lock()
	pending := 0
	for existingState, existing := range h.flows {
		if !now.Before(existing.binding.ExpiresAt) {
			delete(h.flows, existingState)
		} else if existing.agentApproval != "" {
			pending++
		}
	}
	if len(h.flows) >= 128 || pending >= ownerStepUpAgentApprovalFlows {
		h.mu.Unlock()
		stepUpFailure(w)
		return
	}
	h.flows[state] = flow
	h.mu.Unlock()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {localowner.StepUpClientID}, "redirect_uri": {h.origin + localowner.StepUpCallbackPath}, "scope": {"openid"}, "state": {state}, "nonce": {nonce}, "prompt": {"login"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	http.Redirect(w, r, issuer+"/authorize?"+query.Encode(), http.StatusSeeOther)
}

func (h *ownerStepUpHandler) finishAgentApproval(w http.ResponseWriter, ctx context.Context, service *localowner.Service, code string, flow ownerStepUpFlow) {
	idToken, err := service.ExchangeStepUpIDToken(ctx, h.origin, code, flow.verifier)
	if err != nil {
		stepUpFailure(w)
		return
	}
	if _, err = identity.VerifyOwnerStepUp(h.root, idToken, flow.nonce, time.Now()); err != nil {
		stepUpFailure(w)
		return
	}
	decided, err := mcpagents.Open(h.root).Decide(flow.agentApproval, flow.approve)
	if err != nil {
		stepUpFailure(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ownerStepUpAgentApprovalDone.Execute(w, decided)
}
