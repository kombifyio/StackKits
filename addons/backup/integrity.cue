// Package backup - Integrity verification & restore drills.
//
// Two complementary mechanisms:
//
//   1. Repository validation (`kopia repository validate-provider`):
//      Cheap, weekly. Confirms the storage backend still serves the bytes
//      Kopia thinks it wrote — catches B2/S3/Storagebox-side rot, deleted
//      objects, and credential drift.
//
//   2. Restore drill (full content roundtrip, monthly):
//      Picks a random snapshot, restores it into tmpfs (or a throwaway
//      volume), hashes the contents, and compares against the manifest
//      Kopia stored at backup time. A mismatch raises the same notify
//      channel as a failed backup.
//
// Both jobs are scheduled by the addon's own cron. The user does not
// configure anything here — they are wired up automatically when the
// addon is enabled. Failures fan out via #NotifyConfig channels.

package backup

// #IntegrityConfig is the internal contract for the validation jobs.
// It is composed automatically from #Config; not user-facing.
#IntegrityConfig: {
	// Weekly provider validation.
	validateProvider: {
		enabled:  bool | *true
		schedule: string | *"0 5 * * 0" // Sunday 05:00
		// Read a fraction of the data to detect bit-rot without
		// pulling the entire repo every week.
		readDataPercent: int | *2 & >=0 & <=100
	}

	// Monthly restore drill (see #RestoreDrillConfig in addon.cue).
	restoreDrill: {
		enabled:  bool | *true
		schedule: string | *"0 4 1 * *" // 1st of month, 04:00
		target:   *"tmpfs" | "volume"
		// Maximum size of the snapshot to restore. Larger snapshots
		// are sampled (subset path restore) to avoid blowing up tmpfs.
		maxSizeBytes: int | *(1024 * 1024 * 1024) // 1 GiB
	}

	// Heartbeat into the monitoring addon. The integrity jobs push their
	// own pass/fail signal so a stuck cron does not silently disable
	// verification.
	heartbeat: {
		enabled: bool | *true
		// Service name reported to the monitoring stack.
		service: string | *"backup-integrity"
	}
}

// #IntegrityJobService runs both jobs as a sidecar to the kopia-agent.
// Single container, single cron, single set of credentials — no extra
// surface for the user.
#IntegrityJobService: {
	name:        "kopia-integrity"
	displayName: "Kopia Integrity & Restore Drill"
	image:       "kopia/kopia:0.18"
	category:    "backup"

	placement: {
		nodeType: "main"
		strategy: "single"
	}

	volumes: [
		{name: "kopia-config", path: "/app/config", type: "volume"},
		{name: "kopia-cache", path: "/app/cache", type: "volume"},
		// tmpfs for restore drill; sized by orchestrator at apply time.
		{name: "drill-tmpfs", path: "/drill", type: "tmpfs"},
	]

	environment: {
		KOPIA_PASSWORD: =~"^secret://"
	}
}
