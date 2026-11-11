package standaloneoperations

// operatorOperations projects the remaining owner-facing CLI capabilities.
// Their MCP tools are generated from these contracts: IDs and tool names
// follow the CLI path, Arguments carry every input an agent needs, and a
// mutation always requires the exact operation confirmation plus local Owner
// approval before the adapter adds a CLI approval flag such as --owner-approve.
var operatorOperations = []Contract{
	{
		ID: "stackkit.application.inspect", ToolName: "stackkit_application_inspect", Title: "Assess application adoption", Description: "Use before adopting an application that already runs on this host. Reads the container, its current configuration and what must be preserved; changes nothing.", Command: []string{"application", "inspect", "--json"}, Idempotent: true,
		Arguments: []Argument{positionalArg("workload", "exact selected application workload"), requiredFlagArg("container_id", ArgumentString, "--container-id", "exact immutable 64-character Docker container ID"), flagArg("owner_file", ArgumentString, "--owner-file", "workspace-relative private existing native Owner grant file")},
	},
	{
		ID: "stackkit.application.adopt", ToolName: "stackkit_application_adopt", Title: "Adopt application", Description: "Use after stackkit_application_inspect to put an existing application under lifecycle custody. Signs the reviewed binding and keeps its accounts, configuration and data.", Command: []string{"application", "adopt", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("workload", "exact selected application workload"), requiredFlagArg("container_id", ArgumentString, "--container-id", "exact inspected immutable Docker container ID"), requiredFlagArg("plan_digest", ArgumentString, "--plan-digest", "reviewed source and authority adoption digest"), requiredFlagArg("operation_id", ArgumentString, "--operation-id", "stable adoption idempotency identifier"), flagArg("owner_file", ArgumentString, "--owner-file", "workspace-relative private existing native Owner grant file")},
	},
	{ID: "stackkit.application.verify", ToolName: "stackkit_application_verify", Title: "Verify adopted application", Description: "Use after adoption, or when custody looks wrong. Checks the signed binding against the current source; reads only.", Command: []string{"application", "verify", "--json"}, Idempotent: true, Arguments: []Argument{positionalArg("workload", "exact adopted application workload"), flagArg("owner_file", ArgumentString, "--owner-file", "workspace-relative private existing native Owner grant file")}},
	{ID: "stackkit.application.release", ToolName: "stackkit_application_release", Title: "Release adopted application", Description: "Use to hand an adopted application back to manual management. Releases lifecycle custody and keeps the application and its data running.", Command: []string{"application", "release", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true, Arguments: []Argument{positionalArg("workload", "exact adopted application workload"), requiredFlagArg("operation_id", ArgumentString, "--operation-id", "stable release idempotency identifier")}},
	{ID: "stackkit.application.control", ToolName: "stackkit_application_control", Title: "Control adopted application", Description: "Use to start, stop or restart an adopted application without changing it. Acts on the exact adopted container; nothing is recreated and no configuration changes.", Command: []string{"application", "control", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true, Arguments: []Argument{positionalArg("workload", "exact adopted application workload"), requiredFlagArg("action", ArgumentString, "--action", "start, stop or restart"), requiredFlagArg("operation_id", ArgumentString, "--operation-id", "stable power-control idempotency identifier")}},
	// Node OS maintenance (host updates and reboot). The MCP confirmation and
	// local Owner approval stand in for the CLI's interactive --yes.
	{ID: "stackkit.host.updates.plan", ToolName: "stackkit_host_updates_plan", Title: "Plan host updates", Description: "Use before updating packages, to see what would change. Refreshes the package index and lists pending updates with a digest; installs nothing.", Command: []string{"host", "updates", "plan", "--json"}, Idempotent: true},
	{
		ID: "stackkit.host.updates.apply", ToolName: "stackkit_host_updates_apply", Title: "Apply host updates",
		Description: "Use with the digest from stackkit_host_updates_plan. Installs exactly that reviewed package set on this node and reports version changes; a stale plan is refused.",
		Command:     []string{"host", "updates", "apply", "--json", "--yes"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("plan_digest", ArgumentString, "--plan-digest", "plan_digest of the reviewed plan (sha256:<hex>)"),
		},
	},
	{
		ID: "stackkit.host.reboot", ToolName: "stackkit_host_reboot", Title: "Reboot host",
		Description: "Use after updates when the node must restart. Schedules a guarded reboot that waits for running package work; a control-plane node or one that cannot boot unattended is refused.",
		Command:     []string{"host", "reboot", "--json", "--yes"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			flagArg("delay", ArgumentString, "--delay", "time until the reboot starts, 1s to 5m (default 30s)"),
		},
	},
	// Game servers on the installed Game workload (ADR-0043). The CLI derives
	// the Panel keys from owner custody; no key crosses the MCP transcript.
	{ID: "stackkit.game.list", ToolName: "stackkit_game_list", Title: "List game servers", Description: "Use first for game servers: it names the identifiers the other game tools take. Lists the owner's game servers with state and port; reads only.", Command: []string{"game", "list", "--json"}, Idempotent: true},
	{
		ID: "stackkit.game.power", ToolName: "stackkit_game_power", Title: "Game server power",
		Description: "Use with an identifier from stackkit_game_list. Starts, stops or restarts one game server and waits until the new state is observed.",
		Command:     []string{"game", "power", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("server", "game server identifier from stackkit_game_list"),
			{Name: "signal", Kind: ArgumentString, Flag: "--signal", Required: true, Enum: []string{"start", "stop", "restart"}, Description: "power signal"},
		},
	},
	{
		ID: "stackkit.game.allow", ToolName: "stackkit_game_allow", Title: "Admit game player",
		Description: "Use to let one player onto an allow-list game server. Adds the account and reads the allow list back; only that server's list changes.",
		Command:     []string{"game", "allow", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("server", "game server identifier from stackkit_game_list"),
			requiredFlagArg("player", ArgumentString, "--player", "exact game account name (Minecraft account name or Xbox gamertag)"),
		},
	},
	// Add-ons, addresses and application delivery.
	{ID: "stackkit.address.plan", ToolName: "stackkit_address_plan", Title: "Plan public addresses", Description: "Use before binding a public address, to see what registration needs. Prints the account-free address plan; changes nothing.", Command: []string{"address", "plan"}, Idempotent: true},
	{
		ID: "stackkit.address.bind", ToolName: "stackkit_address_bind", Title: "Bind public addresses",
		Description: "Use after an address is allocated to apply it. Writes the bound public routes into a new validated spec file; existing intent is replaced only when its exact current hash is passed.",
		Command:     []string{"address", "bind"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			flagArg("prefix", ArgumentString, "--prefix", "allocated DNS-safe subdomain prefix; exactly one of prefix or zone"),
			flagArg("zone", ArgumentString, "--zone", "allocated install zone label; exactly one of prefix or zone"),
			requiredFlagArg("output", ArgumentString, "--output", "workspace path for the validated bound StackSpec"),
			flagArg("expected_spec_hash", ArgumentString, "--expected-spec-hash", "exact current CUE-normalized spec hash required to replace existing intent"),
		},
	},
	{ID: "stackkit.app.compatibility", ToolName: "stackkit_app_compatibility", Title: "Application compatibility", Description: "Use before choosing an application or a delivery adapter. Shows the fixed support matrix; it reports product capability, not what this plan or host runs.", Command: []string{"app", "compatibility", "--json"}, Idempotent: true},

	// Advanced lifecycle.
	{ID: "stackkit.advanced.trust.inspect", ToolName: "stackkit_advanced_trust_inspect", Title: "Inspect Advanced trust", Description: "Use before any Advanced operation to see which issuer this host trusts. Reads the verified local trust; changes nothing.", Command: []string{"advanced", "trust", "inspect", "--json"}, Idempotent: true},
	{
		ID: "stackkit.advanced.trust.import", ToolName: "stackkit_advanced_trust_import", Title: "Import Advanced trust",
		Description: "Use to trust one known Advanced issuer. Imports the exact pinned bundle into local owner trust and replaces the record that was there.",
		Command:     []string{"advanced", "trust", "import", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("bundle_file", ArgumentString, "--bundle", "path to the public issuer trust bundle"),
			requiredFlagArg("expect_sha256", ArgumentString, "--expect-sha256", "exact sha256:<hex> digest of the bundle"),
		},
	},
	{
		ID: "stackkit.advanced.change-set.create", ToolName: "stackkit_advanced_change_set_create", Title: "Create Advanced change set",
		Description: "Use before an Advanced apply to turn a candidate spec into something reviewable. Writes a content-addressed, Owner-signed change set; applies nothing.",
		Command:     []string{"advanced", "change-set", "create", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to a canonical stackkit.advanced-capability/v1 file"),
			requiredFlagArg("candidate_spec", ArgumentString, "--candidate-spec", "path to the candidate StackSpec v2 using generation.target=terramate"),
		},
	},
	{
		ID: "stackkit.advanced.restore-drill.run", ToolName: "stackkit_advanced_restore_drill_run", Title: "Run restore drill",
		Description: "Use to prove a backup restores without risking the live homelab. Creates or reuses an anchor, stages and verifies a restore, then removes the staging copy; the live runtime is left as it was.",
		Command:     []string{"advanced", "restore-drill", "run", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to a canonical stackkit.advanced-capability/v1 file that allows restore.drill"),
			flagArg("anchor", ArgumentString, "--anchor", "existing sha256 snapshot-anchor ID; a new backup anchor is created when omitted"),
			flagArg("operation_id", ArgumentString, "--operation-id", "stable lowercase drill ID; generated when omitted"),
		},
	},
	{
		ID: "stackkit.advanced.rollback.run", ToolName: "stackkit_advanced_rollback_run", Title: "Run coordinated rollback",
		Description: "Use when an Advanced apply must be undone. Rolls every local stack back to one verified checkpoint, newest first; stacks added after the checkpoint are destroyed.",
		Command:     []string{"advanced", "rollback", "run", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to a canonical stackkit.advanced-capability/v1 file that allows rollback.coordinated"),
			requiredFlagArg("to", ArgumentString, "--to", "target sha256 executor-state snapshot ID, or the sha256 change-set ID whose pre-apply checkpoint is the target"),
		},
	},
	{
		ID: "stackkit.advanced.change-set.apply", ToolName: "stackkit_advanced_change_set_apply", Title: "Apply Advanced change set",
		Description: "Use after the change set is created and reviewed. Applies it and verifies the result, rolling back automatically on failure; the running stack changes.",
		Command:     []string{"advanced", "change-set", "apply", "--json"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to the capability used to create the change set"),
			requiredFlagArg("candidate_spec", ArgumentString, "--candidate-spec", "path to the exact candidate StackSpec v2"),
			requiredFlagArg("change_set", ArgumentString, "--change-set", "exact sha256:<hex> change-set content address"),
			requiredFlagArg("expect_sha256", ArgumentString, "--expect-sha256", "exact sha256:<hex> digest of the stored change-set bytes"),
		},
	},

	// Backup and recovery beyond the core snapshot lifecycle.
	{ID: "stackkit.backup.configure", ToolName: "stackkit_backup_configure", Title: "Configure backup repository", Description: "Use once per host, or after the backup policy changes. Configures the local repository from the generated policy, whose path and service cannot be overridden.", Command: []string{"backup", "configure", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{
		ID: "stackkit.backup.restore.activate", ToolName: "stackkit_backup_restore_activate", Title: "Activate restore",
		Description: "Use after a restore is staged and reviewed. Copies the staged data into the live volumes, taking an automatic safety snapshot first.",
		Command:     []string{"backup", "restore", "activate", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("restore_result_id", "exact staged restore result ID"),
			flagArg("operation_id", ArgumentString, "--operation-id", "stable idempotency key for this activation"),
			flagArg("expected_plan_hash", ArgumentString, "--expected-plan-hash", "admitted lowercase sha256 ResolvedPlan; optional for standalone Owner operation"),
		},
	},
	{
		ID: "stackkit.backup.restore.recover", ToolName: "stackkit_backup_restore_recover", Title: "Recover restore",
		Description: "Use when a restore activation was interrupted. Rolls back before the commit or finishes the committed result; repeating it returns the original result.",
		Command:     []string{"backup", "restore", "recover", "--json", "--owner-approve", "--rollback"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("activation_operation_id", "exact interrupted activation operation ID"),
			flagArg("expected_plan_hash", ArgumentString, "--expected-plan-hash", "original journal's admitted lowercase sha256 ResolvedPlan; optional for standalone Owner operation"),
			flagArg("original_release", ArgumentString, "--original-release", "exact originally admitted release; requires original archive/index digests"),
			flagArg("original_archive_sha256", ArgumentString, "--original-archive-sha256", "original release archive digest for historical recovery"),
			flagArg("original_index_sha256", ArgumentString, "--original-index-sha256", "original signed release-index digest for historical recovery"),
		},
	},
	{
		ID: "stackkit.backup.emergency-export", ToolName: "stackkit_backup_emergency_export", Title: "Emergency export",
		Description: "Use before a move or a disaster, when snapshots are not enough. Writes a new age-encrypted portable archive with checksums and a runbook; it copies files, so stop writers for a consistent export.",
		Command:     []string{"backup", "emergency-export", "--json"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("recipients", ArgumentStringList, "--recipient", "age X25519 recipient public keys"),
			requiredFlagArg("target", ArgumentString, "--target", "new output directory whose parent exists"),
			enumFlagArg("format", "--format", "portable archive format", "tar.gz.age"),
			enumFlagArg("large_media_mode", "--large-media-mode", "handling of large media sources", "manifest-only", "include", "exclude"),
			flagArg("sources", ArgumentStringList, "--source", "explicit CLASS=PATH sources; omit to export the generated backup contract"),
		},
	},
	{
		ID: "stackkit.backup.emergency-restore", ToolName: "stackkit_backup_emergency_restore", Title: "Emergency restore",
		Description: "Use to inspect or stage a portable archive on another host. Decrypts and verifies it into a new staging directory; nothing is activated.",
		Command:     []string{"backup", "emergency-restore", "--json"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("archive", ArgumentString, "--archive", "path to the encrypted emergency archive"),
			requiredFlagArg("identity_file", ArgumentString, "--identity-file", "path to the local age identity file; the key itself never crosses MCP"),
			requiredFlagArg("target", ArgumentString, "--target", "new isolated restore directory whose parent exists"),
			flagArg("max_bytes", ArgumentInteger, "--max-bytes", "maximum decompressed archive bytes"),
		},
	},
	{ID: "stackkit.backup.target.status", ToolName: "stackkit_backup_target_status", Title: "Backup target status", Description: "Use when a backup target looks unreachable, or to check its custody. Verifies local target custody and returns opaque references only; no remote storage is contacted.", Command: []string{"backup", "target", "status"}, Idempotent: true},

	// Break-glass recovery material, referenced by path only.
	{
		ID: "stackkit.break-glass.list", ToolName: "stackkit_break_glass_list", Title: "List break-glass bundles",
		Description: "Use during an outage to find the offline recovery bundles. Lists the encrypted break-glass bundles on this host; reads only.",
		Command:     []string{"break-glass", "list"}, Idempotent: true,
		Arguments: []Argument{flagArg("dir", ArgumentString, "--dir", "break-glass bundle directory")},
	},
	{
		ID: "stackkit.break-glass.show-bundle", ToolName: "stackkit_break_glass_show_bundle", Title: "Show break-glass bundle",
		Description: "Use after stackkit_break_glass_list to get the exact file. Prints the path of one node's still-encrypted bundle; decrypts nothing.",
		Command:     []string{"break-glass", "show-bundle"}, Idempotent: true,
		Arguments: []Argument{
			positionalArg("node", "node name"),
			flagArg("dir", ArgumentString, "--dir", "break-glass bundle directory"),
		},
	},

	// Host contract.
	{ID: "stackkit.compat", ToolName: "stackkit_compat", Title: "Compatibility diagnostics", Description: "Use before installing on an unusual host or operating system. Shows the published support evidence and runs non-destructive host diagnostics; it certifies no server.", Command: []string{"compat"}, Idempotent: true},
	{ID: "stackkit.prepare", ToolName: "stackkit_prepare", Title: "Prepare check", Description: "Use before init or apply to see whether this machine is ready. Runs the read-only host checks that apply also runs; installs nothing.", Command: []string{"prepare", "--json", "--non-interactive"}, Idempotent: true},
	{
		ID: "stackkit.host.attach-conformance", ToolName: "stackkit_host_attach_conformance", Title: "Attach host conformance",
		Description: "Use before planning for a host that is not this machine. Attaches the host binding and its conformance receipt to the inventory; changes only that inventory file.",
		Command:     []string{"host", "attach-conformance"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("inventory", ArgumentString, "--inventory", "workspace-confined inventory.json to update"),
			requiredFlagArg("binding_file", ArgumentString, "--binding", "workspace-confined ExternalHostBinding JSON"),
			requiredFlagArg("receipt_file", ArgumentString, "--receipt", "workspace-confined HostConformanceReceipt JSON"),
		},
	},
	{
		ID: "stackkit.host.conformance", ToolName: "stackkit_host_conformance", Title: "Host conformance receipt",
		Description: "Use to measure a host against the binding used for planning. Produces a provider-neutral conformance receipt on stdout or in a workspace file; installs nothing.",
		Command:     []string{"host", "conformance"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("binding_file", ArgumentString, "--binding", "local ExternalHostBinding JSON"),
			flagArg("output", ArgumentString, "--output", "receipt destination; omit for JSON on stdout"),
		},
	},
	{ID: "stackkit.host.environment", ToolName: "stackkit_host_environment", Title: "Host environment", Description: "Use before init to know which addresses fit. Detects whether this host looks like a home network or a public server; observes only.", Command: []string{"host", "environment", "--json"}, Idempotent: true},
	{
		ID: "stackkit.host.preflight", ToolName: "stackkit_host_preflight", Title: "Host preflight",
		Description: "Use before apply to check whether this host can run the selected StackKit. Measures against the kit's floor and changes nothing; apply repeats the same check.",
		Command:     []string{"host", "preflight", "--json"}, Idempotent: true,
		Arguments: []Argument{
			flagArg("resolved_plan", ArgumentString, "--resolved-plan", "verified canonical ResolvedPlan whose host listeners to check"),
			flagArg("local_node", ArgumentString, "--local-node", "exact local Node in the supplied ResolvedPlan"),
			enumFlagArg("policy", "--policy", "admission policy", "strict", "warn", "skip"),
		},
	},
	{ID: "stackkit.host.prepare", ToolName: "stackkit_host_prepare", Title: "Prepare Cloud host", Description: "Use to converge a Cloud Kit host after an interrupted install; installers and apply run it themselves. Provisions the execution-channel Linux account, its privileges and the workspace-held SSH key.", Command: []string{"host", "prepare"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
	{
		ID: "stackkit.host.remediate", ToolName: "stackkit_host_remediate", Title: "Remediate host",
		Description: "Use when preflight names a finding that has a fix. Plans the reversible fixes, or carries out one named fix; without apply nothing changes.",
		Command:     []string{"host", "remediate", "--json", "--yes"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			flagArg("apply", ArgumentString, "--apply", "named resolution to carry out; omit both apply and auto_reversible to only plan"),
			flagArg("auto_reversible", ArgumentBoolean, "--auto-reversible", "carry out every undoable fix that may run unattended"),
			enumFlagArg("policy", "--policy", "preflight policy used to find findings", "strict", "warn"),
		},
	},

	// Drift reconciliation.
	{
		ID: "stackkit.drift.reconcile", ToolName: "stackkit_drift_reconcile", Title: "Reconcile drift",
		Description: "Use when stackkit_drift reports a difference. Checkpoints, then generates, applies and verifies, rolling back on failure; the running stack changes.",
		Command:     []string{"drift", "reconcile", "--json", "--owner-approve"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			enumFlagArg("mode", "--mode", "reconcile mode", "standard", "advanced"),
			flagArg("capability_file", ArgumentString, "--capability", "advanced: canonical offline capability file"),
			flagArg("candidate_spec", ArgumentString, "--candidate-spec", "advanced: exact Terramate candidate StackSpec"),
			flagArg("change_set", ArgumentString, "--change-set", "advanced: exact Owner-signed change-set ID"),
			flagArg("expect_sha256", ArgumentString, "--expect-sha256", "advanced: exact stored change-set byte digest"),
		},
	},

	// Federation. Inputs are Owner-reviewed public documents or opaque refs.
	{
		ID: "stackkit.federation.binding.adopt", ToolName: "stackkit_federation_binding_adopt", Title: "Adopt Federation binding",
		Description: "Use to bring an external host into this homelab's inventory. Validates, Owner-signs and adopts the binding in one step.",
		Command:     []string{"federation", "binding", "adopt"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("binding_file", ArgumentString, "--binding", "external binding JSON"),
			flagArg("inventory", ArgumentString, "--inventory", "local Inventory path"),
			flagArg("resolved_plan", ArgumentString, "--resolved-plan", "local canonical ResolvedPlan path"),
		},
	},
	{
		ID: "stackkit.federation.binding.import", ToolName: "stackkit_federation_binding_import", Title: "Import Federation binding",
		Description: "Use to record a binding that is already Owner-signed. Admits it into local inventory; no remote system changes.",
		Command:     []string{"federation", "binding", "import"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("admission_file", ArgumentString, "--admission", "Owner-signed binding admission JSON"),
			flagArg("inventory", ArgumentString, "--inventory", "local Inventory path"),
			flagArg("resolved_plan", ArgumentString, "--resolved-plan", "local canonical ResolvedPlan path"),
		},
	},
	{
		ID: "stackkit.federation.control.bind", ToolName: "stackkit_federation_control_bind", Title: "Bind Federation control receiver",
		Description: "Use to point this receiver at a Home. Admits the Home's public authority and the local receiver custody, replacing the previous receiver authority.",
		Command:     []string{"federation", "control", "bind"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "Owner-reviewed receiver custody JSON without private keys")},
	},
	{
		ID: "stackkit.federation.control.sign", ToolName: "stackkit_federation_control_sign", Title: "Sign Federation control action",
		Description: "Use to sign one plan or verify action for a Cloud node. Signs with the current Home custody and returns the action; nothing is sent.",
		Command:     []string{"federation", "control", "sign"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "exact action JSON with plan hash, target and bounded timestamps")},
	},
	{
		ID: "stackkit.federation.control.send", ToolName: "stackkit_federation_control_send", Title: "Send Federation control action",
		Description: "Use to deliver a signed action to a Cloud node. Sends it over the exact pinned Home-initiated connection, so the remote node is reached.",
		Command:     []string{"federation", "control", "send"}, Mutation: true, OpenWorld: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("file", ArgumentString, "--file", "Home-signed action JSON"),
			requiredFlagArg("endpoint_file", ArgumentString, "--endpoint-file", "local pinned mTLS endpoint and confined credential paths"),
		},
	},
	{ID: "stackkit.federation.control.withdraw", ToolName: "stackkit_federation_control_withdraw", Title: "Withdraw Federation control receiver", Description: "Use to cut a receiver off for good. Withdraws receiver authority and ends its TLS sessions; the withdrawal stands on later runs.", Command: []string{"federation", "control", "withdraw"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
	{
		ID: "stackkit.federation.link.bind", ToolName: "stackkit_federation_link_bind", Title: "Bind Federation link",
		Description: "Use to adopt an existing WireGuard fabric link. Adopts the handles into local owner custody; the remote fabric is not changed.",
		Command:     []string{"federation", "link", "bind"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "external fabric custody JSON without private keys")},
	},
	{
		ID: "stackkit.federation.link.stop", ToolName: "stackkit_federation_link_stop", Title: "Stop Federation link",
		Description: "Use to take an adopted link down temporarily. Stops the interface and keeps the external fabric, so nothing is deleted.",
		Command:     []string{"federation", "link", "stop"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("fabric_ref", ArgumentString, "--fabric-ref", "exact adopted opaque fabric reference")},
	},

	// Local identity plane.
	{
		ID: "stackkit.identity.projection.inspect", ToolName: "stackkit_identity_projection_inspect", Title: "Inspect identity projection",
		Description: "Use before approving an identity projection. Verifies the signed, credential-free document; changes nothing.",
		Command:     []string{"identity", "projection", "inspect", "--json"}, Idempotent: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "signed projection JSON")},
	},
	{
		ID: "stackkit.identity.projection.approve", ToolName: "stackkit_identity_projection_approve", Title: "Approve identity projection",
		Description: "Use after stackkit_identity_projection_inspect. Creates the Owner-signed approval and leaves PocketID untouched; stackkit_identity_projection_apply makes it live.",
		Command:     []string{"identity", "projection", "approve", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "signed projection JSON")},
	},
	{
		ID: "stackkit.identity.projection.apply", ToolName: "stackkit_identity_projection_apply", Title: "Apply identity projection",
		Description: "Use after the projection is approved to make it live. Applies that exact projection to local PocketID; local accounts and groups change.",
		Command:     []string{"identity", "projection", "apply", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("projection_sha256", ArgumentString, "--projection-sha256", "exact approved projection digest")},
	},
	{
		ID: "stackkit.identity.projection.unlink", ToolName: "stackkit_identity_projection_unlink", Title: "Unlink identity projection",
		Description: "Use to detach optional identity sync. Detaches the linked projection without deleting any local identity.",
		Command:     []string{"identity", "projection", "unlink", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("projection_sha256", ArgumentString, "--projection-sha256", "exact linked projection digest")},
	},
	{
		ID: "stackkit.identity.workload-peer.request", ToolName: "stackkit_identity_workload_peer_request", Title: "Request workload peer",
		Description: "Use first on a workload host that needs a Home certificate. Creates or reuses the local key and returns only the public request; no admission changes.",
		Command:     []string{"identity", "workload-peer", "request", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity"),
			flagArg("rotate_key", ArgumentBoolean, "--rotate-key", "generate a new key while preserving the active credential until install"),
		},
	},
	{
		ID: "stackkit.identity.workload-peer.enroll", ToolName: "stackkit_identity_workload_peer_enroll", Title: "Enroll workload peer",
		Description: "Use with that request to issue a credential. Issues and admits one workload peer for an installed Home publication; returns only public certificates.",
		Command:     []string{"identity", "workload-peer", "enroll", "--owner-approve"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity"),
			requiredFlagArg("file", ArgumentString, "--file", "public peer request JSON"),
			requiredFlagArg("server_name", ArgumentString, "--server-name", "installed Home origin server name"),
			flagArg("replace_key", ArgumentBoolean, "--replace-key", "explicitly replace the selected peer key"),
		},
	},
	{
		ID: "stackkit.identity.workload-peer.install", ToolName: "stackkit_identity_workload_peer_install", Title: "Install workload peer",
		Description: "Use to install the issued certificate on the workload host. Writes that peer's credential under a Home root verified through a separate channel.",
		Command:     []string{"identity", "workload-peer", "install", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("file", ArgumentString, "--file", "public peer credential JSON"),
			requiredFlagArg("home_root_sha256", ArgumentString, "--home-root-sha256", "Home root sha256 fingerprint verified through a separate Owner-approved channel"),
		},
	},
	{
		ID: "stackkit.identity.workload-peer.revoke", ToolName: "stackkit_identity_workload_peer_revoke", Title: "Revoke workload peer",
		Description: "Use when a workload certificate should stop working. Withdraws the peer's current Home admission; the old certificate is no longer accepted.",
		Command:     []string{"identity", "workload-peer", "revoke", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity")},
	},
	{
		ID: "stackkit.identity.workload-peer.probe", ToolName: "stackkit_identity_workload_peer_probe", Title: "Probe workload peer",
		Description: "Use to check that a workload answers through the tunnel. Requests the origin through an existing loopback socket; reads only.",
		Command:     []string{"identity", "workload-peer", "probe"}, Idempotent: true,
		Arguments: []Argument{
			requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity"),
			requiredFlagArg("address", ArgumentString, "--address", "existing loopback Federation socket address"),
		},
	},

	// Releases, logs and migration.
	{
		ID: "stackkit.kit.list", ToolName: "stackkit_kit_list", Title: "List releases",
		Description: "Use to find which StackKits releases exist. Fetches the public release index; reads only.",
		Command:     []string{"kit", "list", "--json"}, Idempotent: true, OpenWorld: true,
		Arguments: []Argument{enumFlagArg("channel", "--channel", "release channel", "stable", "beta", "edge")},
	},
	{
		ID: "stackkit.logs.read", ToolName: "stackkit_logs_read", Title: "Read logs",
		Description: "Use after stackkit_logs to read one run. Returns one page of structured events, newest by default; changes nothing.",
		Command:     []string{"logs", "--json"}, Idempotent: true,
		Arguments: []Argument{
			optionalPositionalArg("run_id", "run ID; omit for the newest log"),
			flagArg("decisions", ArgumentBoolean, "--decisions", "only decision events"),
			flagArg("errors", ArgumentBoolean, "--errors", "only errors and warnings"),
			flagArg("timing", ArgumentBoolean, "--timing", "timing summary"),
			flagArg("cursor", ArgumentString, "--cursor", "opaque cursor returned by a prior page"),
			flagArg("max_events", ArgumentInteger, "--max-events", "maximum structured events per page"),
			flagArg("max_bytes", ArgumentInteger, "--max-bytes", "maximum raw log bytes scanned per page"),
		},
	},
	{
		ID: "stackkit.migrate", ToolName: "stackkit_migrate", Title: "Migrate StackSpec v1",
		Description: "Use to move an older stack-spec.yaml forward. Classifies it and reports what must be done by hand; a new file is written only when asked, and nothing is upgraded in place.",
		Command:     []string{"migrate"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			optionalPositionalArg("v1_spec_file", "StackSpec v1 path; defaults to the workspace spec"),
			flagArg("target_kit", ArgumentString, "--target-kit", "explicit target KitProfile"),
			flagArg("complete_with", ArgumentString, "--complete-with", "full explicit StackSpec v2 candidate path"),
			flagArg("inventory", ArgumentString, "--inventory", "observed Inventory path the completion resolves against"),
			flagArg("output", ArgumentString, "--output", "write the migration result to this new workspace path instead of stdout"),
			flagArg("spec_output", ArgumentString, "--spec-output", "write the completed canonical StackSpec v2 to this new workspace path"),
			enumFlagArg("format", "--format", "output format", "json", "yaml"),
		},
	},

	// Module authoring.
	{
		ID: "stackkit.module.lint", ToolName: "stackkit_module_lint", Title: "Lint modules",
		Description: "Use before submitting a module change. Lints module CUE for pin, health, security and access hygiene; changes nothing.",
		Command:     []string{"module", "lint", "--json"}, Idempotent: true,
		Arguments: []Argument{
			flagArg("module", ArgumentString, "--module", "path to one module directory"),
			flagArg("all", ArgumentBoolean, "--all", "lint every module under modules_dir"),
			flagArg("modules_dir", ArgumentString, "--modules-dir", "root modules directory"),
			flagArg("strict", ArgumentBoolean, "--strict", "fail on any error finding"),
		},
	},
	{
		ID: "stackkit.module.scaffold", ToolName: "stackkit_module_scaffold", Title: "Scaffold module",
		Description: "Use to start a new module from its facts file. Renders the module artifacts or prints them; existing files are overwritten only with force.",
		Command:     []string{"module", "scaffold"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("facts_file", ArgumentString, "--facts", "path to module_facts.json"),
			flagArg("out", ArgumentString, "--out", "output module directory"),
			flagArg("force", ArgumentBoolean, "--force", "overwrite existing files"),
			flagArg("print", ArgumentBoolean, "--print", "return the rendered artifacts instead of writing them"),
			flagArg("catalog", ArgumentString, "--catalog", "compiled CUE catalog.json for imageSource references"),
		},
	},

	// Output transactions and the embedded registry.
	{
		ID: "stackkit.output-transaction.gc-retired", ToolName: "stackkit_output_transaction_gc_retired", Title: "Collect retired output transaction",
		Description: "Use to clean up after a retired output transaction. Inspects the tombstone by default and removes it only when apply is set.",
		Command:     []string{"output-transaction", "gc-retired"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("transaction_id", ArgumentString, "--transaction-id", "exact lowercase transaction ID"),
			flagArg("action", ArgumentString, "--action", "exact action reported by a prior inspection; required with apply"),
			flagArg("apply", ArgumentBoolean, "--apply", "execute the echoed action; inspection is the default"),
		},
	},
	{ID: "stackkit.registry.info", ToolName: "stackkit_registry_info", Title: "Registry info", Description: "Use to inspect the catalog copy bundled with this binary. Summarizes the embedded registry snapshot; reads only.", Command: []string{"registry", "info"}, Idempotent: true},

	// Use-case authoring and scoped application release changes. Authoring
	// changes intent only by spec-hash CAS; normal generate/plan/apply executes.
	{
		ID: "stackkit.use-cases.add", ToolName: "stackkit_use_cases_add", Title: "Add use case",
		Description: "Use to add one application from the kit catalog. Writes it into the spec and prepares its local secret slots; nothing is deployed until apply.",
		Command:     []string{"use-cases", "add", "--json"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: append([]Argument{
			positionalArg("slug", "use case declared by the installed kit"),
			flagArg("alternative", ArgumentString, "--alternative", "catalog alternative; omission uses the CUE default"),
			flagArg("platform", ArgumentString, "--platform", "runtime adapter; omission uses the CUE default"),
			flagArg("node", ArgumentString, "--node", "existing enabled node; required when the intent has several nodes"),
		}, useCaseChangeArguments...),
	},
	{
		ID: "stackkit.use-cases.configure", ToolName: "stackkit_use_cases_configure", Title: "Configure use case",
		Description: "Use to change the settings or profiles of one chosen application. Merges them into the spec and keeps everything omitted; no service changes until apply.",
		Command:     []string{"use-cases", "configure", "--json"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: append([]Argument{positionalArg("slug", "selected use case")}, useCaseChangeArguments...),
	},
	{
		ID: "stackkit.use-cases.upgrade", ToolName: "stackkit_use_cases_upgrade", Title: "Change application release",
		Description: "Use to move one application to another admitted release. Stops it and checkpoints its data for rollback; the next apply starts the new release and finalize drops the checkpoint.",
		Command:     []string{"use-cases", "upgrade", "--json", "--owner-approve"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			optionalPositionalArg("slug", "selected use case; required with to"),
			flagArg("to", ArgumentString, "--to", "admitted target release; exactly one of to, rollback or finalize"),
			flagArg("rollback", ArgumentString, "--rollback", "release operation ID to return to its prior release and data"),
			flagArg("finalize", ArgumentString, "--finalize", "release operation ID whose retained data is removed"),
			flagArg("resume_operation_id", ArgumentString, "--operation-id", "resume this interrupted checkpoint with the same slug and to"),
			flagArg("expected_spec_hash", ArgumentString, "--expected-spec-hash", "require this canonical current intent hash for to"),
		},
	},
	{
		ID: "stackkit.use-cases.upgrade.status", ToolName: "stackkit_use_cases_upgrade_status", Title: "Application release status",
		Description: "Use to see where an application release change stands. Reads the Owner-signed record, the retained volumes and their read-back; changes nothing.",
		Command:     []string{"use-cases", "upgrade", "--json"}, Idempotent: true,
		Arguments: []Argument{requiredFlagArg("operation_id", ArgumentString, "--status", "release operation ID")},
	},
	{
		ID: "stackkit.capabilities.configure", ToolName: "stackkit_capabilities_configure", Title: "Configure capability",
		Description: "Use to enable or change one declared Foundation capability. Merges public settings, a provider and custody references into the spec by hash; no service changes until apply.",
		Command:     []string{"capabilities", "configure", "--json"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: append([]Argument{
			positionalArg("capability", "kit-declared capability ID"),
			flagArg("enable", ArgumentBoolean, "--enable", "explicitly enable this optional capability"),
			flagArg("provider", ArgumentString, "--provider", "CUE provider; omission retains the selection"),
		}, intentChangeArguments...),
	},

	// Secret custody, without secret values.
	{ID: "stackkit.secrets.materialize", ToolName: "stackkit_secrets_materialize", Title: "Materialize secret custody", Description: "Use after adding a workload and before stackkit_generate. Establishes local custody for every declared secret; values are never returned.", Command: []string{"secrets", "materialize"}, Mutation: true, Idempotent: true, OwnerApproval: true},

	// Plan-declared services.
	{
		ID: "stackkit.service.logs", ToolName: "stackkit_service_logs", Title: "Service logs",
		Description: "Use to read what one service logged. Returns bounded, redacted entries for one managed service; changes nothing.",
		Command:     []string{"service", "logs", "--json"}, Idempotent: true,
		Arguments: []Argument{
			positionalArg("service_key", "managed service key"),
			flagArg("tail", ArgumentInteger, "--tail", "number of redacted log entries (1..200)"),
			flagArg("cursor", ArgumentString, "--cursor", "opaque cursor from a previous page"),
		},
	},
	{
		ID: "stackkit.service.start", ToolName: "stackkit_service_start", Title: "Start service",
		Description: "Use to bring back a service that was stopped. Starts one service declared by the active plan; only that service changes.",
		Command:     []string{"service", "start", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("service_key", "managed service key")},
	},
	{
		ID: "stackkit.service.stop", ToolName: "stackkit_service_stop", Title: "Stop service",
		Description: "Use to take one non-critical service offline. Stops only that service; core services such as base, id and auth are refused.",
		Command:     []string{"service", "stop", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("service_key", "managed service key")},
	},
	{
		ID: "stackkit.service.restart", ToolName: "stackkit_service_restart", Title: "Restart service",
		Description: "Use to restart one managed service without touching its data. Restarts only that service; the rest of the stack keeps running.",
		Command:     []string{"service", "restart", "--json", "--owner-approve"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("service_key", "managed service key")},
	},

	// Support evidence.
	{
		ID: "stackkit.support.export", ToolName: "stackkit_support_export", Title: "Export support evidence",
		Description: "Use to hand one run to support. Writes that rollout log and its receipts to a new local file; no runtime changes.",
		Command:     []string{"support", "export"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			optionalPositionalArg("run_id", "run ID; omit for the newest log"),
			requiredFlagArg("output", ArgumentString, "--output", "new local JSON file to create"),
		},
	},

	// Household users in local PocketID. Setup and activation URLs never cross MCP.
	{ID: "stackkit.user.list", ToolName: "stackkit_user_list", Title: "List users", Description: "Use before adding or removing a household member. Lists household users in local PocketID; reads only.", Command: []string{"user", "list", "--json"}, Idempotent: true},
	{ID: "stackkit.user.owner.status", ToolName: "stackkit_user_owner_status", Title: "Owner activation status", Description: "Use when the owner cannot sign in. Reads the owner passkey activation status without its activation link; changes nothing.", Command: []string{"user", "owner", "status", "--json"}, Idempotent: true},
	{
		ID: "stackkit.user.remove", ToolName: "stackkit_user_remove", Title: "Remove user",
		Description: "Use to remove a household member for good. Revokes their devices and sessions and deletes the PocketID user; the owner and admins are refused.",
		Command:     []string{"user", "remove", "--json", "--owner-approve"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("username", "household username")},
	},
}

// intentChangeArguments are the retained-intent authoring inputs shared by
// use-case and capability changes. Secret inputs are secret:// references to
// existing local custody, never secret material.
var intentChangeArguments = []Argument{
	flagArg("settings", ArgumentStringList, "--setting", "public setting key=JSON; merged by name"),
	flagArg("custody_refs", ArgumentStringList, "--secret-ref", "input key=reference to existing local custody, never secret material"),
	flagArg("expected_spec_hash", ArgumentString, "--expected-spec-hash", "require this canonical current intent hash; omitted uses the hash read before authoring"),
	flagArg("output", ArgumentString, "--output", "new candidate file that retains the active baseline; required for Advanced or multi-node intent"),
}

var useCaseChangeArguments = append([]Argument{
	flagArg("compute_profile", ArgumentString, "--compute-profile", "module-local low, standard or high; omission retains the selection"),
	flagArg("storage_profile", ArgumentString, "--storage-profile", "catalog module storage profile; omission retains the selection"),
	flagArg("accelerator_profile", ArgumentString, "--accelerator-profile", "catalog module accelerator profile; omission retains the selection"),
}, intentChangeArguments...)

func flagArg(name string, kind ArgumentKind, flag, description string) Argument {
	return Argument{Name: name, Kind: kind, Flag: flag, Description: description}
}

func requiredFlagArg(name string, kind ArgumentKind, flag, description string) Argument {
	argument := flagArg(name, kind, flag, description)
	argument.Required = true
	return argument
}

func enumFlagArg(name, flag, description string, values ...string) Argument {
	argument := flagArg(name, ArgumentString, flag, description)
	argument.Enum = values
	return argument
}

func positionalArg(name, description string) Argument {
	return Argument{Name: name, Kind: ArgumentString, Required: true, Description: description}
}

func optionalPositionalArg(name, description string) Argument {
	return Argument{Name: name, Kind: ArgumentString, Description: description}
}
