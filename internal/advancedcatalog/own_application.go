package advancedcatalog

import "github.com/kombifyio/stackkits/internal/advancedcapability"

// ownApplicationOperations operate one own application (techstack.own-application,
// including one taken over with "Stack it") like a catalog application: logs,
// status, start, stop, restart, a tag update with automatic rollback, and the
// backup that holds its volume. A mutation is reviewed first: its dry run
// returns the review digest the execution names. On a Techstack-managed node an
// executed start, stop, restart, update or rollback needs an
// own-application.lifecycle capability bound to the application, the action,
// the operation ID and the review digest of its dry run (at most 15 minutes);
// a standalone node runs it under Owner approval alone.
func ownApplicationOperations() []Operation {
	result := []Outcome{{Status: "success", ContractRef: ref("stackkit.own-application-result/v1", "internal/ownapplication/state.go"), Description: "Raw own-application result (no command-result envelope): outcome reviewed, observed, observation_unavailable, started, stopped or restarted, or refused, pending or denied with reason; the signed record, review digest and, for logs, redacted text."}}
	update := []Outcome{{Status: "success", ContractRef: ref("stackkit.own-application-update/v1", "internal/ownapplication/image_update.go"), Description: "Raw tag update result (no command-result envelope): outcome reviewed, updated, refused, rolled_back (prior image and checkpoint data both back), rollback_failed or discarded with reasonCode, allowMajor, targetCheck not_checked_by_kombify, prior and target image, the data checkpoint (state planned, taken, retained, restored, kept or discarded, retainedBytes, and the exact restoreCommand and discardCommand) and the update and rollback results."}}
	base := []string{"app", "own", "--name", "{ownApplicationName}"}
	read := func(operation, summary string, argv []string, results []Outcome) Operation {
		return Operation{Operation: operation, Status: StatusAvailable, SinceRelease: SincePending, Summary: summary, Command: "stackkit app own", Argv: append(append([]string{}, base...), argv...), OptionalArgs: []OptionalArgs{}, Requires: Requirements{}, Inputs: []Input{}, Results: results, Events: []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed}}
	}
	mutate := func(operation, summary string, argv []string, results []Outcome) Operation {
		return Operation{Operation: operation, Status: StatusAvailable, SinceRelease: SincePending, Summary: summary, Command: "stackkit app own", Argv: append(append(append([]string{}, base...), argv...), "--review-digest", "{reviewDigest}", "--operation-id", "{operationId}", "--owner-approve", "--json"),
			OptionalArgs: []OptionalArgs{{Argv: []string{"--capability", "{capabilityFile}"}, Description: "Managed node: the Techstack own-application.lifecycle capability for exactly this application, action, operation ID and review digest (at most 15 minutes); required there, refused on a standalone node."}},
			Mutates:      true, Requires: Requirements{Capability: true, CapabilityOperation: advancedcapability.OperationOwnApplicationLifecycle, TrustImported: true, OwnerApproval: true},
			Inputs: []Input{capabilityInput}, Results: append(append([]Outcome{}, results...), denialOutcome), Events: []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionCapability}}
	}
	operations := []Operation{
		read("own-application.status", "Read the signed own-application record and its runtime: container state and health, route answer and firewall grant. Changes nothing.", []string{"--action", "status", "--json"}, result),
		read("own-application.logs", "Read the last 100 log lines of every container of the own application; secret values are redacted.", []string{"--action", "logs", "--json"}, result),
		read("own-application.review", "Review start, stop, restart or rollback: returns the review digest bound to the current signed record, installation and, for a firewalled application, the freshly observed host rules. Changes nothing.", []string{"--action", "{ownApplicationAction}", "--dry-run", "--json"}, result),
		mutate("own-application.start", "Start a stopped own application from its signed bundle and admitted local image; a firewalled application starts only through the verified host firewall watcher (never docker start around it).", []string{"--action", "start"}, result),
		mutate("own-application.stop", "Stop the own application; its data, secrets and firewall grant stay, and its route closes.", []string{"--action", "stop"}, result),
		mutate("own-application.restart", "Stop and start the running own application again through the same path as start.", []string{"--action", "restart"}, result),
		read("own-application.update.review", "Review a tag update: the same image repository, a newer version tag with the same major and variant (--allow-major admits another major or a running image that names no version), resolved to its registry digest. Returns resolvedImage, the review digest and replacesCheckpoint (state and size of the retained copy this update's checkpoint replaces); the target is not checked by kombify.", []string{"--action", "update", "--image-tag", "{imageTag}", "--dry-run", "--json"}, update),
		{
			Operation: "own-application.update", Status: StatusAvailable, SinceRelease: SincePending,
			Summary: "Update to the reviewed tag: re-resolve its digest and refuse a moved tag before stopping; for an application with a volume stop it and take an application-scoped data checkpoint (free space measured before anything stops; an operation-bound copy of its volumes read back by a digest over contents, mode, owner, symlink targets and empty directories; fails closed without the installed backup helper; refused while an earlier checkpoint is kept or unfinished); recreate on the new image and require it running and, with an image healthcheck, healthy through the stability window. On any failure stop it, restore the checkpoint data, return to the prior image and verify again: rolled_back only when image and data are both back, otherwise rollback_failed with the checkpoint and its exact restore command. The target is not checked by kombify.",
			Command: "stackkit app own",
			Argv: append(append([]string{}, base...), "--action", "update", "--image-tag", "{imageTag}", "--resolved-image", "{resolvedImage}",
				"--review-digest", "{reviewDigest}", "--operation-id", "{operationId}", "--owner-approve", "--json"),
			OptionalArgs: []OptionalArgs{
				{Argv: []string{"--allow-major"}, Description: "The review admitted another major version or an unversioned running image."},
				{Argv: []string{"--capability", "{capabilityFile}"}, Description: "Managed node: the Techstack own-application.lifecycle capability for exactly this application, action, operation ID and review digest (at most 15 minutes); required there, refused on a standalone node."},
			},
			Mutates: true, Requires: Requirements{Capability: true, CapabilityOperation: advancedcapability.OperationOwnApplicationLifecycle, TrustImported: true, OwnerApproval: true},
			Inputs: []Input{capabilityInput}, Results: append(append([]Outcome{}, update...), denialOutcome), Events: []EventPhase{},
			Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionCapability},
		},
		mutate("own-application.rollback", "Return to the prior image held in signed custody; the data stays as it is (own-application.checkpoint-rollback restores a tag update's data as well).", []string{"--action", "rollback"}, result),
		read("own-application.checkpoint-rollback.review", "Review restoring the data checkpoint a tag update took and returning to the image it recorded; returns the review digest. Changes nothing.", []string{"--action", "rollback", "--checkpoint", "{checkpointOperationId}", "--dry-run", "--json"}, update),
		read("own-application.checkpoint-discard.review", "Review removing the retained copy of a tag update's data checkpoint (its size is checkpoint.retainedBytes); returns the review digest. Changes nothing.", []string{"--action", "discard-checkpoint", "--checkpoint", "{checkpointOperationId}", "--dry-run", "--json"}, update),
		mutate("own-application.checkpoint-discard", "Remove the retained copy of a tag update's data checkpoint, also one a failed rollback kept (the owner then accepts the live data as it is); refused while its update has not finished (checkpoint_in_flight). State, review digest, removal and the signed record are checked and written under one lifecycle lock. A kept or unfinished checkpoint blocks new tag updates and remove --delete-data until it is restored or discarded.", []string{"--action", "discard-checkpoint", "--checkpoint", "{checkpointOperationId}"}, update),
		mutate("own-application.checkpoint-rollback", "Stop the application, restore the data checkpoint the tag update took (read back by digest), return to its prior image and verify it; the manual form of the automatic rollback and the restore command a rollback_failed result names.", []string{"--action", "rollback", "--checkpoint", "{checkpointOperationId}"}, update),
		{
			Operation: "own-application.backup", Status: StatusAvailable, SinceRelease: SincePending,
			Summary: "Take a node backup snapshot now; it holds the volume of every own application with data under the installation's existing user-content coverage. Changes no intent.",
			Command: "stackkit backup run", Argv: []string{"backup", "run", "--operation-id", "{operationId}", "--json"}, OptionalArgs: []OptionalArgs{},
			Mutates: true, Requires: Requirements{}, Inputs: []Input{},
			Results: []Outcome{{Status: "success", ContractRef: ref(CommandResultSchemaVersion, CommandResultSchema), Description: "data.id is the snapshot anchor ID for backup restore."}},
			Events:  []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed},
		},
	}
	return operations
}
