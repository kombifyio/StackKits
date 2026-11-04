package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Routes of the Authority's node API that a device client calls with its own
// credential and proof (or, to enroll, its person's passkey sign-in) instead
// of the node API key. The notice, confirmation and undo routes are what the
// Hub, the Companion and the State Console deliver to people.
const (
	HomeEnrollmentBootstrapPath = "/api/v1/identity/home/enroll/bootstrap"
	HomeDevicesPath             = "/api/v1/identity/home/devices"
	HomeNoticesPath             = "/api/v1/identity/home/notices"
	homeEnrollmentsPrefix       = "/api/v1/identity/home/enrollments/"
	homeDeviceRevokeSuffix      = "/revoke"
	homeNoticeUndoSuffix        = "/undo"
	homeEnrollmentConfirmSuffix = "/confirm"
	// HomeOwnerEnrollConfirmPath is the Owner's confirmation page for an
	// away enrollment on the Authority's step-up origin; it takes ?id=<device
	// ID> and is served only when the Owner is the confirmer.
	HomeOwnerEnrollConfirmPath = "/api/v1/identity/step-up/enroll-confirm"

	homeIdentityMaxBodyBytes = 16 << 10
)

// RegisterHomeIdentityRoutes mounts the device-facing identity routes:
//
//	GET  HomeEnrollmentBootstrapPath                     the LAN enrollment bootstrap (no secret)
//	GET  HomeDevicesPath                                 the caller's devices; the Owner's devices see all
//	POST HomeDevicesPath/{id}/revoke                     revoke own device (Owner: any) through the chain
//	GET  HomeNoticesPath                                 enrollment notices of the caller's account (Owner: all)
//	POST HomeNoticesPath/{id}/undo                       undo an at-home enrollment or decline a pending one
//	POST /api/v1/identity/home/enrollments/{id}/confirm  confirm a pending enrollment from an own device:
//	                                                     {"challenge", "stepUp"} with the person's passkey step-up
//
// Every route but the bootstrap needs "Authorization: DPoP <credential>" and
// a DPoP proof for exactly its method and path.
func RegisterHomeIdentityRoutes(mux *http.ServeMux, root string) {
	mux.HandleFunc("GET "+HomeEnrollmentBootstrapPath, func(w http.ResponseWriter, _ *http.Request) {
		bootstrap, err := LoadHomeEnrollmentBootstrap(root)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "enrollment_unavailable", false)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, bootstrap)
	})
	mux.Handle("GET "+HomeDevicesPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, _ *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		devices, err := ListHomeDevices(root)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		visible := []HomeDevice{}
		for _, device := range devices {
			if actor.Owner || device.AccountSubject == actor.Account {
				visible = append(visible, device)
			}
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"devices": visible})
	}))
	mux.Handle("POST "+HomeDevicesPath+"/{id}"+homeDeviceRevokeSuffix, homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		if !decodeHomeJSON(r.Body, homeIdentityMaxBodyBytes, &struct{}{}, true) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		revocation, err := RevokeHomeDevice(root, r.PathValue("id"), actor, time.Now())
		writeHomeRevocation(w, revocation, err)
	}))
	mux.Handle("GET "+HomeNoticesPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, _ *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		notices, err := ListHomeEnrollmentNotices(root, actor, time.Now())
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"notices": notices})
	}))
	mux.Handle("POST "+HomeNoticesPath+"/{id}"+homeNoticeUndoSuffix, homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		if !decodeHomeJSON(r.Body, homeIdentityMaxBodyBytes, &struct{}{}, true) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		revocation, err := UndoHomeEnrollment(root, r.PathValue("id"), actor, time.Now())
		writeHomeRevocation(w, revocation, err)
	}))
	mux.Handle("POST "+homeEnrollmentsPrefix+"{id}"+homeEnrollmentConfirmSuffix, homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, verifier *HomeVerifier, caller HomeAuthentication, _ HomeActor) {
		var body struct {
			Challenge string `json:"challenge"`
			StepUp    string `json:"stepUp"`
		}
		if !decodeHomeJSON(r.Body, homeIdentityMaxBodyBytes, &body, false) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		device, err := verifier.ConfirmEnrollmentFromDevice(caller, r.PathValue("id"), body.Challenge, body.StepUp)
		switch {
		case errors.Is(err, ErrHomeNoticeNotFound):
			denyHomeIdentity(w, http.StatusNotFound, "not_found", false)
		case errors.Is(err, ErrHomeDeviceCapReached):
			denyHomeIdentity(w, http.StatusConflict, "device_cap_reached", false)
		case errors.Is(err, ErrHomeRegistryFull):
			denyHomeIdentity(w, http.StatusInsufficientStorage, "registry_full", false)
		case err != nil:
			denyHomeIdentity(w, http.StatusForbidden, "step_up_required", false)
		default:
			writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"device": device})
		}
	}))
}

// IsHomeIdentityDeviceRoute reports the identity routes that authenticate a
// device proof or a person's passkey sign-in themselves (or serve only the
// public enrollment bootstrap), so the node API key does not apply to them.
func IsHomeIdentityDeviceRoute(r *http.Request) bool {
	path := r.URL.Path
	switch r.Method {
	case http.MethodGet:
		return path == HomeEnrollmentBootstrapPath || path == HomeDevicesPath || path == HomeNoticesPath
	case http.MethodPost:
		if path == HomeEnrollmentPath || path == HomeDeviceRenewalPath {
			return true
		}
		return homeRouteSegment(path, HomeDevicesPath+"/", homeDeviceRevokeSuffix) ||
			homeRouteSegment(path, HomeNoticesPath+"/", homeNoticeUndoSuffix) ||
			homeRouteSegment(path, homeEnrollmentsPrefix, homeEnrollmentConfirmSuffix)
	}
	return false
}

// homeRouteSegment matches prefix + one non-empty path segment + suffix.
func homeRouteSegment(path, prefix, suffix string) bool {
	middle, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return false
	}
	middle, ok = strings.CutSuffix(middle, suffix)
	return ok && middle != "" && !strings.Contains(middle, "/")
}

// homeDeviceAuthenticated admits a request only with the current credential
// of an active device and a fresh proof for exactly this method and path,
// then hands the verified caller to handle. The caller acts as the Owner only
// when its account is the Owner of the current Home trust.
func homeDeviceAuthenticated(root string, handle func(http.ResponseWriter, *http.Request, *HomeVerifier, HomeAuthentication, HomeActor)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifier, err := HomeVerifierFor(root)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "workspace_unavailable", false)
			return
		}
		credential, proof, err := ParseHomeDeviceAuthorization(r.Header)
		if err != nil {
			denyHomeIdentity(w, http.StatusUnauthorized, "current_device_proof_unavailable", false)
			return
		}
		target := proofTarget(proof)
		origin, ok := homeIdentityRouteOrigin(target, r.URL.Path)
		if !ok || !homeIdentityOriginAdmitted(root, target, r.URL.Path) {
			denyHomeIdentity(w, http.StatusUnauthorized, "current_device_proof_unavailable", false)
			return
		}
		caller, err := verifier.Authenticate(HomeDeviceRequest{Method: r.Method, RouteOrigin: origin, Path: r.URL.Path, Credential: credential, Proof: proof})
		if err != nil {
			denyHomeIdentity(w, http.StatusUnauthorized, "current_device_proof_unavailable", false)
			return
		}
		actor := HomeActor{Account: caller.Subject, Device: caller.Device}
		if trust, trustErr := loadCurrentHomeTrust(root, time.Now().UTC()); trustErr == nil && trust.PocketID.Subject == caller.Subject {
			actor.Owner = true
		}
		handle(w, r, verifier, caller, actor)
	})
}

// proofTarget reads the htu claim of a proof without verifying it, only to
// find the origin the device addressed; Authenticate then verifies the proof
// against exactly that origin and the request's own path.
func proofTarget(raw string) string {
	if len(raw) == 0 || len(raw) > homeDeviceMaxTokenBytes {
		return ""
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256, jose.EdDSA})
	if err != nil {
		return ""
	}
	var claims struct {
		Target string `json:"htu"`
	}
	if token.UnsafeClaimsWithoutVerification(&claims) != nil {
		return ""
	}
	return claims.Target
}

// homeIdentityRouteOrigin parses a proof target of the Authority's own
// identity routes: an https URL whose path is exactly path, without query or
// fragment, on a canonical origin, which it returns. It does not decide
// whether the origin is this Authority's; homeIdentityOriginAdmitted does.
func homeIdentityRouteOrigin(target, path string) (string, bool) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.RawPath != "" || parsed.Path != path {
		return "", false
	}
	origin := "https://" + parsed.Host
	if validateHomeOrigin(origin) != nil || origin+path != target {
		return "", false
	}
	return origin, true
}

// homeIdentityOriginAdmitted admits a proof target only on one of the
// Authority's own identity origins: the Hub origin of its identity runtime
// custody (https://base.<domain>) and the origins its server configured
// (ConfigureHomeIdentityOrigins). Without any, every proof is refused.
func homeIdentityOriginAdmitted(root, target, path string) bool {
	origin, ok := homeIdentityRouteOrigin(target, path)
	return ok && slices.Contains(homeIdentityOrigins(root), origin)
}

var homeConfiguredOrigins sync.Map

// ConfigureHomeIdentityOrigins sets the origins, besides the Hub origin of
// the identity runtime custody, on which this server serves its identity
// routes (renewal, away enrollment, the person routes), such as its Owner
// step-up origin. A proof for any other origin is refused.
func ConfigureHomeIdentityOrigins(root string, origins ...string) error {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	admitted := []string{}
	for _, origin := range origins {
		if origin == "" {
			continue
		}
		if validateHomeOrigin(origin) != nil {
			return errors.New("identity: an identity origin must be a canonical https origin")
		}
		admitted = append(admitted, origin)
	}
	homeConfiguredOrigins.Store(absolute, admitted)
	return nil
}

func homeIdentityOrigins(root string) []string {
	origins := []string{}
	if absolute, err := filepath.Abs(root); err == nil {
		if configured, ok := homeConfiguredOrigins.Load(absolute); ok {
			origins = append(origins, configured.([]string)...)
		}
	}
	if address, err := homeIdentityAddress(root); err == nil {
		if hub := "https://" + address.ServiceHost("base"); validateHomeOrigin(hub) == nil && !slices.Contains(origins, hub) {
			origins = append(origins, hub)
		}
	}
	return origins
}

func writeHomeRevocation(w http.ResponseWriter, revocation HomeRevocation, err error) {
	switch {
	case errors.Is(err, ErrHomeDeviceNotFound), errors.Is(err, ErrHomeNoticeNotFound):
		denyHomeIdentity(w, http.StatusNotFound, "not_found", false)
	case errors.Is(err, ErrHomeActorInUndoWindow):
		denyHomeIdentity(w, http.StatusForbidden, "acting_device_in_undo_window", false)
	case err != nil:
		denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
	default:
		writeHomeIdentityJSON(w, http.StatusOK, revocation)
	}
}

func writeHomeIdentityJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
