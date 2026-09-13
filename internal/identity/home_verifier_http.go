package identity

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
)

const (
	HomeVerifierPath   = "/api/v1/identity/home/verify"
	HomeEnrollmentPath = "/api/v1/identity/home/enroll"
)

// HomeVerifierHandler is mounted on the existing server. It does not create a
// listener or treat API keys, workload TLS, or forwarded headers as identity.
func HomeVerifierHandler(root string) http.Handler {
	absolute, rootErr := filepath.Abs(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != HomeVerifierPath {
			http.NotFound(w, r)
			return
		}
		if rootErr != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "workspace_unavailable")
			return
		}
		var request struct {
			Method string          `json:"method"`
			Target string          `json:"target"`
			Proof  HomeAccessProof `json:"proof"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, (192<<10)+1))
		if err != nil || len(raw) > 192<<10 {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_proof_envelope")
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_proof_envelope")
			return
		}
		result, err := AuthenticateHomeRequest(absolute, request.Method, request.Target, request.Proof)
		if err != nil {
			denyHomeIdentity(w, http.StatusUnauthorized, "current_human_device_proof_unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// HomeEnrollmentHandler keeps pairing closed until human step-up authority
// exists. Workload peer enrollment is a different owner operation.
func HomeEnrollmentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != HomeEnrollmentPath {
			http.NotFound(w, r)
			return
		}
		_ = EnrollHomeDevice("", nil)
		denyHomeIdentity(w, http.StatusForbidden, "pairing_and_step_up_unbound")
	})
}

func denyHomeIdentity(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	code := "home_identity_denied"
	guidance := "Refresh current Home identity and obtain a new request-bound device proof."
	if reason == "pairing_and_step_up_unbound" {
		code = "home_enrollment_unavailable"
		guidance = "Home device enrollment remains unavailable until local pairing and human step-up authority are connected."
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error_code": code, "reason_code": reason, "retryable": false, "user_guidance": guidance,
	})
}
