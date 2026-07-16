// Package backup - Restic-to-Kopia migration importer.
//
// Backup addon v1.0.0 used Restic. v2.0.0 standardises on Kopia. Users with
// existing Restic repositories should not lose history when upgrading.
//
// The importer is a one-shot job that:
//   1. Reads the Restic password from the existing SOPS+age secret slot.
//   2. Iterates every Restic snapshot (`restic snapshots --json`).
//   3. For each snapshot, mounts it via `restic mount` to a tmpfs and
//      runs `kopia snapshot create` against the same path with
//      `--override-source` and `--start-time` set to the original
//      Restic timestamp. The Kopia snapshot ends up looking exactly
//      like a snapshot Kopia took at that historical time.
//   4. After every snapshot is imported and verified, writes a marker
//      file to the Restic repo (`MIGRATED-TO-KOPIA`) and switches the
//      addon's `engine` field from "restic-import" to "kopia".
//
// Operationally:
//   - The job runs at most once per host. If the marker file is present
//     the importer is a no-op.
//   - Restic and Kopia run side-by-side only for the duration of the
//     import. Once complete, the Restic agent is removed.
//   - Failure mode: any error aborts. The addon keeps running on Restic
//     until the user fixes the cause and re-triggers
//     `stackkit backup migrate-from-restic`.

package backup

// #ResticImportConfig is selected when #Config.engine == "restic-import".
// It is intentionally minimal: the importer needs nothing the user doesn't
// already have configured for the existing Restic repo.
#ResticImportConfig: {
	// Where the existing Restic repository lives. The same value the v1
	// addon used.
	resticRepository: string

	// SOPS+age-encrypted Restic password.
	resticPassword: =~"^secret://"

	// After-import behaviour.
	// "keep"      — leave the Restic repo untouched (default).
	// "archive"   — rename it to ${repo}-pre-kopia for safety.
	// "delete"    — purge after a successful Kopia validate-provider.
	postImportAction: *"keep" | "archive" | "delete"

	// Hard cap on how long the importer is allowed to run before raising
	// an alert. Big repos (> 1 TB) may need to extend this.
	maxRuntimeHours: int | *24
}

// #ResticImporterService runs once and exits. It is registered with a
// systemd-style `restart: on-failure` policy in the generator so a
// transient B2/Storagebox blip doesn't abandon the migration.
#ResticImporterService: {
	name:        "restic-importer"
	displayName: "Restic-to-Kopia Importer (one-shot)"
	image:       "ghcr.io/kombify/restic-kopia-importer:0.1"
	category:    "backup"

	placement: {
		nodeType: "main"
		strategy: "single"
	}

	volumes: [
		{name: "kopia-config", path: "/app/config", type: "volume"},
		{name: "kopia-cache", path: "/app/cache", type: "volume"},
		{host: "/backup", path: "/backup", type: "bind"},
		// tmpfs used as the Restic mount target during import.
		{name: "restic-mount", path: "/mnt/restic", type: "tmpfs"},
	]

	environment: {
		RESTIC_REPOSITORY: string
		RESTIC_PASSWORD:   =~"^secret://"
		KOPIA_PASSWORD:    =~"^secret://"
	}
}
