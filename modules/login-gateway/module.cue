// Package login_gateway — Platform-level forward-auth enforcement.
//
// Ensures every L3 service in a StackKit sits behind TinyAuth (ForwardAuth middleware)
// with PocketID as the OIDC IdP. This is a V6 invariant: no L3 service is exposed
// over HTTP(S) without the forward-auth middleware, unless explicitly annotated
// `exposed-to-public: true` (reserved for services that do their own auth).
//
// login-gateway is a glue module: it does not run its own container. It declares
// the middleware as provided (so CUE Decision Logic can reject any L3 service
// that opts out without permission) and wires TinyAuth + PocketID together.
//
// STATUS: Scaffolded for V6 Phase 2. Enforcement in Composition Engine (Phase 1).
package login_gateway

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "login-gateway"
		displayName: "Login Gateway"
		version:     "0.1.0"
		layer:       "L2-platform-identity"
		description: "Enforces TinyAuth forward-auth in front of every L3 service, with PocketID as OIDC IdP. Mandatory platform invariant in V6."
		// Draft: scaffolded glue module (V6 Phase 2), no owned services yet.
		maturity: "draft"
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy", "forwardauth-host"]
			}
			tinyauth: {
				minVersion: "4.0"
				provides: ["forwardauth", "authentication"]
			}
			pocketid: {
				provides: ["oidc-provider"]
			}
		}
		infrastructure: {
			docker:  true
			network: "shared"
		}
	}

	provides: {
		capabilities: {
			"forward-auth-enforcement": true
			"oidc-session":             true
			"l3-protection":            true
		}
		middleware: {
			"login-gateway": {
				type:               "forwardauth"
				address:            "http://tinyauth:3000/api/auth/traefik"
				trustForwardHeader: true
				authResponseHeaders: [
					"remote-user", "remote-sub", "remote-name",
					"remote-email", "remote-groups",
				]
			}
		}
	}

	settings: {
		perma: {
			// The Traefik middleware name applied to every L3 service by default.
			middlewareName: *"login-gateway@file" | string

			// Escape hatch: L3 services may set `exposed-to-public: true` in their
			// compose labels to skip this middleware. Allowed modules are whitelisted
			// here; anything else MUST go through login-gateway.
			allowedPublicBypass: *[] | [...string]
		}
		flexible: {
			// Session cookie expiry — matches TinyAuth default.
			sessionExpiry: *86400 | int & >=300

			// Require MFA for admin-group users on login.
			requireMfaForAdmins: *true | bool
		}
	}

	contexts: {
		local: {
			_secureCookie: false
		}
		cloud: {
			_secureCookie: true
		}
		pi: {
			_secureCookie: false
		}
	}

	// No owned services — login-gateway glues TinyAuth + PocketID.
	services: {}
}
