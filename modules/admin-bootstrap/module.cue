// Package admin_bootstrap — Bootstrap the initial admin user in LLDAP + PocketID.
//
// Runs after LLDAP and PocketID are healthy. Creates:
// - One admin user in LLDAP (groups: admins, users)
// - Matching OIDC claim mapping in PocketID
// - A one-time password, printed to CLI stdout ONLY (no file, no secret service).
//
// The user is expected to rotate the password on first login at https://auth.<domain>.
// If the password is lost before rotation, `stackkit admin reset-password` regenerates it.
//
// STATUS: Scaffolded for V6 Phase 2. Provisioner implementation pending.
package admin_bootstrap

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "admin-bootstrap"
		displayName: "Admin Bootstrap"
		version:     "0.1.0"
		layer:       "L1-foundation"
		description: "Bootstraps the initial admin user in LLDAP + PocketID. Initial password printed to CLI stdout, must be rotated on first login."
		testScenarios: ["SK-S3", "SK-S4", "SK-S5"]
	}

	requires: {
		services: {
			lldap: {
				minVersion: "0.5.0"
				provides: ["ldap-directory"]
			}
			pocketid: {
				provides: ["oidc-provider"]
			}
		}
		infrastructure: {
			docker:            true
			persistentStorage: true
			network:           "shared"
		}
	}

	provides: {
		capabilities: {
			"admin-user-bootstrap": true
			"oidc-claim-mapping":   true
		}
	}

	settings: {
		perma: {
			// Admin username — changing requires teardown of LLDAP state.
			adminUsername: *"admin" | =~"^[a-z][a-z0-9_-]{2,31}$"

			// Groups the admin user is added to.
			adminGroups: *["admins", "users"] | [...string]
		}
		flexible: {
			// Admin email — used for OIDC claim and password-reset flow.
			adminEmail: *"admin@example.com" | =~"^[^@]+@[^@]+\\.[^@]+$"

			// Admin display name.
			adminDisplayName: *"Administrator" | string

			// Password length (bytes of entropy; final string is base64url-encoded).
			passwordEntropyBytes: *32 | int & >=16 & <=128
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	// No persistent services; this module is a one-shot provisioner.
	services: {}

	provisioners: {
		"lldap-bootstrap": {
			image:     "alpine/curl:latest"
			command:   "sh -c 'echo admin-bootstrap provisioner: Phase 2 implementation pending'"
			dependsOn: "lldap"
			networks: ["frontend"]
			environment: {
				LLDAP_URL:          "http://lldap:17170"
				ADMIN_USERNAME:     "{{.admin_username}}"
				ADMIN_EMAIL:        "{{.admin_email}}"
				ADMIN_DISPLAY_NAME: "{{.admin_display_name}}"
			}
		}
		"pocketid-bootstrap": {
			image:     "alpine/curl:latest"
			command:   "sh -c 'echo admin-bootstrap provisioner: Phase 2 implementation pending'"
			dependsOn: "pocketid"
			networks: ["frontend"]
			environment: {
				POCKETID_URL:   "http://pocketid:1411"
				ADMIN_USERNAME: "{{.admin_username}}"
				ADMIN_EMAIL:    "{{.admin_email}}"
			}
		}
	}
}
