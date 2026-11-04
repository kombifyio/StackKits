// Package foundation - Break-glass and Owner recovery schemas
package foundation

// #BreakGlassConfig is the StackSpec emergency-path selection.
#BreakGlassConfig: {
	enabled:        bool | *true
	scope:          "full-emergency-admin" | *"full-emergency-admin"
	pocketidAdmin:  bool | *true
	tinyauthStatic: bool | *true
	serverRecovery: bool | *true
}

// #OwnerRecoveryPolicy is the Owner recovery of the homelab identity target
// (§9, §12, §14.9), realized by internal/identity breakglass.go and
// bundle.go. The Owner holds a recovery key and a recovery code, each shown
// once at setup; StackKits keeps only the key's public recipient and a slow
// verifier of the code, and kombify never receives the key. There is no
// break-glass account and no long-lived setup token.
#OwnerRecoveryPolicy: {
	recoveryKey: {
		algorithm:         "age-mlkem768x25519"
		shownOnce:         true
		storedByStackKits: false
		storedByKombify:   false
	}
	recoveryCode: {
		singleUse: true
		// At the Authority's local console the code registers a new Owner
		// passkey at once.
		consoleDelaySeconds: 0
		// From an enrolled Owner device only after this delay, with a notice to
		// every Owner device; the console or any Owner device can cancel.
		deviceDelaySeconds:         86400
		deviceRequestWindowSeconds: 259200
		deviceFailuresPerHour:      5
		passkeyLinkSeconds:         900
	}
	// A member without passkeys gets a passkey reset from the Owner
	// (stackkit user activate --reset).
	memberPasskeyReset: "owner"
	breakGlassSetupToken: false
}

// #BreakGlassBundle is the recovery set as stored: one file per Authority,
// written locally and to the Owner's backup target (a directory or the bound
// S3 target, object StackKitsRecoverySet/<site>/<node>.age).
#BreakGlassBundle: {
	nodeName:   =~"^[a-zA-Z0-9][a-zA-Z0-9-]*$"
	bundlePath: string | *"/var/lib/stackkit/recovery/break-glass-\(nodeName).age"
	encryption: {
		algorithm:              "age-mlkem768x25519"
		compression:            "gzip"
		archive:                "tar"
		manifestAuthentication: "hmac-sha256-hkdf-recovery-key"
	}
	contents: #IdentityRecoverySet
}

// #IdentityRecoverySet is the manifest.json entry of a recovery set.
// Mirrors internal/identity/bundle.go RecoverySetManifest.
#IdentityRecoverySet: {
	schema:         "stackkit.identity-recovery-set/v1"
	createdAt:      =~"^\\d{4}-\\d{2}-\\d{2}T"
	ownerRef:       string & !=""
	siteRef:        string & !=""
	nodeRef:        string & !=""
	identityOrigin: =~"^https://"
	recipient:      =~"^age1pq1"
	files: [...#IdentityRecoverySetEntry]
	volumes: [...#IdentityRecoverySetVolume]
}

#IdentityRecoverySetEntry: {
	path:    string & !=""
	dir?:    bool
	mode:    int & >=0 & <=0o777
	uid:     int
	gid:     int
	size:    int & >=0
	sha256?: =~"^[0-9a-f]{64}$"
}

#IdentityRecoverySetVolume: {
	name:         "pocketid-data" | "step-ca-db"
	dockerVolume: =~"_(pocketid-data|step-ca-db)$"
	entries: [...#IdentityRecoverySetEntry]
}

#RecoveryMaterialHandoff: {
	kind:  "hash" | "ref"
	value: string

	if kind == "hash" {
		value: =~"^\\$argon2id\\$.*"
	}
	if kind == "ref" {
		value: =~"^(techstack|secret|doppler|vault)://.+|^(env|file):.+"
	}
}
