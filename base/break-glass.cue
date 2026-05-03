// Package base - Break-Glass schemas
package base

#PocketIDBreakGlass: {
	enabled:         bool | *true
	usernamePattern: string | *"bg-{nodename}@local"
	passwordLength:  int & >=24 | *32
	group:           string | *"pocketid_admin"
}

#BreakGlassBundle: {
	nodeName:      =~"^[a-zA-Z0-9][a-zA-Z0-9-]*$"
	bundlePath:    string | *"/var/lib/stackkit/recovery/break-glass-\(nodeName).age"
	plaintextPath: string | *"/var/lib/stackkit/recovery/break-glass-\(nodeName).txt"

	encryption: {
		algorithm: "age-scrypt"
		scryptN:   int | *17 // log2(N), 2^17
		scryptR:   int | *8
		scryptP:   int | *1
	}

	contents:       #BundleContents
	plaintextMode:  string | *"0600"
	plaintextOwner: string | *"root:root"
}

#BundleContents: {
	pocketidAdmin:       bool | *true
	tinyauthStatic:      bool | *true
	nodeMetadata:        bool | *true
	restoreInstructions: bool | *true
}

#BundlePayload: {
	version:     1
	generatedAt: =~"^\\d{4}-\\d{2}-\\d{2}T"
	node: {
		name:        string
		hostname:    string
		clusterRole: "main" | "worker" | "storage"
		pocketidUrl: string
	}
	breakGlass: {
		// PocketID v2 is passkey-only. The Layer-1 break-glass record is a
		// CreateUser + one-time-access-token pair: redeeming the setup URL
		// lets the recoverer enroll a WebAuthn credential and become a
		// fully-privileged admin. No password concept exists.
		// Mirrors internal/identity/bundle.go PocketIDAdminPayload.
		pocketidAdmin: {
			username:   string
			setupToken: string
			setupUrl:   string
			group:      string
			userId:     string
		}
		tinyauthStatic: {username: string, passwordBcrypt: string, passwordPlain: string}
	}
	restoreInstructions: string
}
