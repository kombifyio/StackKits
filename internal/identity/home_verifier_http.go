package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	HomeVerifierPath      = "/api/v1/identity/home/verify"
	HomeEnrollmentPath    = "/api/v1/identity/home/enroll"
	HomeDeviceRenewalPath = "/api/v1/identity/home/device/renew"

	homeRenewalMaxBodyBytes = 16 << 10
)

// ParseHomeDeviceAuthorization reads the device credential from exactly one
// "Authorization: DPoP <credential>" header and the proof from exactly one
// "DPoP" header. Any other shape is refused, never guessed.
func ParseHomeDeviceAuthorization(header http.Header) (credential, proof string, err error) {
	authorization, proofs := header.Values("Authorization"), header.Values("DPoP")
	if len(authorization) != 1 || len(proofs) != 1 {
		return "", "", ErrHomeAccessDenied
	}
	scheme, token, found := strings.Cut(authorization[0], " ")
	if !found || !strings.EqualFold(scheme, "DPoP") || token == "" || strings.ContainsAny(token, " \t,") || proofs[0] == "" || strings.ContainsAny(proofs[0], " \t,") {
		return "", "", ErrHomeAccessDenied
	}
	return token, proofs[0], nil
}

// HomeVerifierHandler is the verification endpoint for a component that
// fronts a route in another process. The caller states the request it
// observed, with the route origin from its own route table. The endpoint sits
// behind the node API boundary; it treats no API key, workload TLS or
// forwarded header as identity.
func HomeVerifierHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != HomeVerifierPath {
			http.NotFound(w, r)
			return
		}
		verifier, err := HomeVerifierFor(root)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "workspace_unavailable", false)
			return
		}
		var request HomeDeviceRequest
		if !decodeHomeJSON(r.Body, 48<<10, &request, false) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_proof_envelope", false)
			return
		}
		result, err := verifier.Authenticate(request)
		if err != nil {
			denyHomeIdentity(w, http.StatusUnauthorized, "current_device_proof_unavailable", false)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// HomeDeviceRenewalHandler renews a device credential on the Authority. The
// request carries the DPoP proof in the "DPoP" header and, when a user
// verification is due or offered, {"userVerification": "<Pocket ID ID
// token>"} as its body. The proof is the authentication; no API key applies.
func HomeDeviceRenewalHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != HomeDeviceRenewalPath {
			http.NotFound(w, r)
			return
		}
		verifier, err := HomeVerifierFor(root)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "workspace_unavailable", false)
			return
		}
		proofs := r.Header.Values("DPoP")
		var body struct {
			UserVerification string `json:"userVerification"`
		}
		if len(proofs) != 1 || !decodeHomeJSON(r.Body, homeRenewalMaxBodyBytes, &body, true) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_renewal_request", false)
			return
		}
		credential, err := verifier.RenewDeviceCredential(HomeRenewalRequest{Method: r.Method, Proof: proofs[0], UserVerification: body.UserVerification})
		switch {
		case errors.Is(err, ErrHomeEnrollmentPending):
			w.Header().Set("Retry-After", "30")
			denyHomeIdentity(w, http.StatusUnauthorized, "enrollment_pending", true)
			return
		case errors.Is(err, ErrHomeUserVerificationRequired):
			w.Header().Set("WWW-Authenticate", `DPoP error="user_verification_required"`)
			denyHomeIdentity(w, http.StatusUnauthorized, "user_verification_required", true)
			return
		case err != nil:
			w.Header().Set("WWW-Authenticate", `DPoP error="invalid_dpop_proof"`)
			denyHomeIdentity(w, http.StatusUnauthorized, "renewal_denied", false)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(credential)
	})
}

// decodeHomeJSON decodes exactly one JSON object with known fields only. An
// empty body is accepted only where it is optional.
func decodeHomeJSON(body io.Reader, limit int64, out any, optional bool) bool {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return false
	}
	if optional && len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out) == nil && decoder.Decode(new(any)) == io.EOF
}

func denyHomeIdentity(w http.ResponseWriter, status int, reason string, retryable bool) {
	guidance := map[string]string{
		"workspace_unavailable":            "The StackKits workspace is unavailable on this node.",
		"invalid_proof_envelope":           "Send one JSON object with method, routeOrigin, path, credential and proof.",
		"current_device_proof_unavailable": "Renew the device credential and send a new request-bound DPoP proof.",
		"invalid_renewal_request":          "Send one DPoP header and an optional JSON object with userVerification.",
		"user_verification_required":       "Sign in with your passkey on this device, then renew with the ID token and a new DPoP proof.",
		"renewal_denied":                   "This device cannot renew. If it was revoked or unused for 180 days, enroll it again with a new key.",
		"enrollment_pending":               "This device waits for its confirmation by your own enrolled device or the Owner. Renew again once it is confirmed.",
		"enrollment_unavailable":           "This server enrolls no devices: it has no LAN enrollment address.",
		"registry_unavailable":             "The device registry is unavailable on this node. Try again shortly.",
		"invalid_request":                  "Send at most one JSON object with the fields of this route.",
		"not_found":                        "No such device, notice or pending enrollment of your account, or its window has passed.",
		"device_cap_reached":               "This account has reached its device limit. Revoke a device you no longer use first.",
		"step_up_required":                 "Sign in with your passkey on this device for exactly this confirmation and send the ID token with its challenge within 60 seconds.",
		"acting_device_in_undo_window":     "A device enrolled at home within the last 24 hours can revoke only itself. Use an older device of yours, or ask the Owner.",
		"registry_full":                    "The device registry is full. Revoke devices that are no longer used.",
		"lan_only":                         "Recovery and Owner approvals work only from the home network. Open the Hub at home.",
		"hub_unavailable":                  "The Hub is not ready on this server: it has no identity runtime address yet. Apply the Basement core first.",
		"origin_refused":                   "Open the Hub on its own address and sign in there.",
		"sign_in_failed":                   "The passkey sign-in could not be completed. Sign in again.",
	}[reason]
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error_code": "home_identity_denied", "reason_code": reason, "retryable": retryable, "user_guidance": guidance,
	})
}
