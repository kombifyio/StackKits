package accessgate

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Gate paths. The LAN router's forward-auth middleware calls PathLAN; the
// remote router binds PathRemote to its only entrypoint.
const (
	PathLAN    = "/v1/gate/lan"
	PathRemote = "/v1/gate/remote"
)

// ServeHTTP answers one forward-auth call. The router sends the original
// method, host and request URI as X-Forwarded-Method, X-Forwarded-Host and
// X-Forwarded-Uri derived from the request itself (trustForwardHeader is off
// on both gate middlewares); everything else about the request is read from
// its own headers.
func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var listener string
	switch r.URL.Path {
	case PathLAN:
		listener = ListenerLAN
	case PathRemote:
		listener = ListenerRemote
	default:
		http.NotFound(w, r)
		return
	}
	header := r.Header.Clone()
	decision := g.Decide(r.Context(), Request{
		Listener: listener,
		Method:   singleHeader(r.Header, "X-Forwarded-Method"),
		Host:     singleHeader(r.Header, "X-Forwarded-Host"),
		URI:      singleHeader(r.Header, "X-Forwarded-Uri"),
		Header:   header,
		HTTP:     r,
	})
	w.Header().Set("Cache-Control", "no-store")
	if decision.Allow {
		for name, values := range decision.Headers {
			w.Header()[name] = values
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	// A launch answer: the router relays this non-2xx response, cookies and
	// Location included, to the browser.
	if decision.Redirect != "" {
		for _, cookie := range decision.Cookies {
			http.SetCookie(w, cookie)
		}
		w.Header().Set("Location", decision.Redirect)
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(decision.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error_code":    "access_gate_denied",
		"reason_code":   decision.Reason,
		"retryable":     decision.Status == http.StatusTooManyRequests,
		"user_guidance": guidance(decision.Reason),
	})
}

// singleHeader refuses a repeated routing header instead of picking one.
func singleHeader(header http.Header, name string) string {
	values := header.Values(name)
	if len(values) != 1 || (name != "X-Forwarded-Uri" && strings.Contains(values[0], ",")) {
		return ""
	}
	return values[0]
}

func guidance(reason string) string {
	switch reason {
	case "route_local_only", "privileged_route_lan_only":
		return "This service is reachable only from your home network or the VPN add-on."
	case "remote_proof_required", "device_proof_required", "device_proof_unverified", "device_verifier_unavailable":
		return "Open this service from the Homelab Hub or a kombify app on a device enrolled to your Homelab account."
	case "group_denied":
		return "Your Homelab account has no access to this service; ask the Owner to grant it."
	case "agent_envelope_unverified":
		return "Agent access needs this homelab linked to your kombify account."
	case "anonymous_rate_limited":
		return "Too many requests; try again shortly."
	case "app_pass_refused":
		return "This app pass is not valid for this service. Create a new one in the Homelab Hub on an enrolled device."
	case "launch_state_mismatch", "launch_refused", "launch_malformed", "browser_sessions_unavailable":
		return "Open this service again from the Homelab Hub on a device enrolled to your Homelab account."
	case "group_membership_unverified":
		return "Your household membership could not be checked right now; try again shortly."
	default:
		return "Access to this route is denied."
	}
}
