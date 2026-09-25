package standaloneoperations

// operatorOperations projects the remaining owner-facing CLI capabilities.
// Their MCP tools are generated from these contracts: IDs and tool names
// follow the CLI path, Arguments carry every input an agent needs, and a
// mutation always requires the exact operation confirmation plus local Owner
// approval before the adapter adds a CLI approval flag such as --owner-approve.
var operatorOperations = []Contract{
	// Game servers on the installed Game workload (ADR-0043). The CLI derives
	// the Panel keys from owner custody; no key crosses the MCP transcript.
	{ID: "stackkit.game.list", ToolName: "stackkit_game_list", Title: "List game servers", Description: "List the owner's game servers on the installed Game workload with state and port.", Command: []string{"game", "list", "--json"}, Idempotent: true},
	{
		ID: "stackkit.game.power", ToolName: "stackkit_game_power", Title: "Game server power",
		Description: "Start, stop or restart one game server and wait for the observed state.",
		Command:     []string{"game", "power", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("server", "game server identifier from stackkit_game_list"),
			{Name: "signal", Kind: ArgumentString, Flag: "--signal", Required: true, Enum: []string{"start", "stop", "restart"}, Description: "power signal"},
		},
	},
	{
		ID: "stackkit.game.allow", ToolName: "stackkit_game_allow", Title: "Admit game player",
		Description: "Admit one player on a StackKits allow-list game server and read the game's allow list back.",
		Command:     []string{"game", "allow", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("server", "game server identifier from stackkit_game_list"),
			requiredFlagArg("player", ArgumentString, "--player", "exact game account name (Minecraft account name or Xbox gamertag)"),
		},
	},
	// Add-ons, addresses and application delivery.
	{ID: "stackkit.address.plan", ToolName: "stackkit_address_plan", Title: "Plan public addresses", Description: "Emit the account-free, secret-free public address registration plan for the current StackSpec.", Command: []string{"address", "plan"}, Idempotent: true},
	{
		ID: "stackkit.address.bind", ToolName: "stackkit_address_bind", Title: "Bind public addresses",
		Description: "Bind an allocated prefix or install zone into canonical public routes and persist the validated StackSpec.",
		Command:     []string{"address", "bind"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			flagArg("prefix", ArgumentString, "--prefix", "allocated DNS-safe subdomain prefix; exactly one of prefix or zone"),
			flagArg("zone", ArgumentString, "--zone", "allocated install zone label; exactly one of prefix or zone"),
			requiredFlagArg("output", ArgumentString, "--output", "workspace path for the validated bound StackSpec"),
			flagArg("expected_spec_hash", ArgumentString, "--expected-spec-hash", "exact current CUE-normalized spec hash required to replace existing intent"),
		},
	},
	{ID: "stackkit.app.compatibility", ToolName: "stackkit_app_compatibility", Title: "Application compatibility", Description: "Show the governed application and delivery-adapter support matrix.", Command: []string{"app", "compatibility", "--json"}, Idempotent: true},

	// Advanced lifecycle.
	{ID: "stackkit.advanced.trust.inspect", ToolName: "stackkit_advanced_trust_inspect", Title: "Inspect Advanced trust", Description: "Inspect the verified local Advanced issuer trust.", Command: []string{"advanced", "trust", "inspect", "--json"}, Idempotent: true},
	{
		ID: "stackkit.advanced.trust.import", ToolName: "stackkit_advanced_trust_import", Title: "Import Advanced trust",
		Description: "Import an exact pinned Advanced issuer trust bundle into local Owner trust.",
		Command:     []string{"advanced", "trust", "import", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("bundle_file", ArgumentString, "--bundle", "path to the public issuer trust bundle"),
			requiredFlagArg("expect_sha256", ArgumentString, "--expect-sha256", "exact sha256:<hex> digest of the bundle"),
		},
	},
	{
		ID: "stackkit.advanced.change-set.create", ToolName: "stackkit_advanced_change_set_create", Title: "Create Advanced change set",
		Description: "Create a content-addressed Owner-signed Terramate change set from an offline capability and a candidate StackSpec.",
		Command:     []string{"advanced", "change-set", "create", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to a canonical stackkit.advanced-capability/v1 file"),
			requiredFlagArg("candidate_spec", ArgumentString, "--candidate-spec", "path to the candidate StackSpec v2 using generation.target=terramate"),
		},
	},
	{
		ID: "stackkit.advanced.restore-drill.run", ToolName: "stackkit_advanced_restore_drill_run", Title: "Run restore drill",
		Description: "Stage and verify a restore of a backup anchor without activation, gated by an offline Advanced capability.",
		Command:     []string{"advanced", "restore-drill", "run", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to a canonical stackkit.advanced-capability/v1 file that allows restore.drill"),
			flagArg("anchor", ArgumentString, "--anchor", "existing sha256 snapshot-anchor ID; a new backup anchor is created when omitted"),
			flagArg("operation_id", ArgumentString, "--operation-id", "stable lowercase drill ID; generated when omitted"),
		},
	},
	{
		ID: "stackkit.advanced.rollback.run", ToolName: "stackkit_advanced_rollback_run", Title: "Run coordinated rollback",
		Description: "Roll every local Terramate stack back to one verified executor-state checkpoint in reverse run order, gated by an offline Advanced capability.",
		Command:     []string{"advanced", "rollback", "run", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to a canonical stackkit.advanced-capability/v1 file that allows rollback.coordinated"),
			requiredFlagArg("to", ArgumentString, "--to", "target sha256 executor-state snapshot ID, or the sha256 change-set ID whose pre-apply checkpoint is the target"),
		},
	},
	{
		ID: "stackkit.advanced.change-set.apply", ToolName: "stackkit_advanced_change_set_apply", Title: "Apply Advanced change set",
		Description: "Checkpoint, apply and verify one exact Owner-signed Terramate change set with automatic verified rollback.",
		Command:     []string{"advanced", "change-set", "apply", "--json"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("capability_file", ArgumentString, "--capability", "path to the capability used to create the change set"),
			requiredFlagArg("candidate_spec", ArgumentString, "--candidate-spec", "path to the exact candidate StackSpec v2"),
			requiredFlagArg("change_set", ArgumentString, "--change-set", "exact sha256:<hex> change-set content address"),
			requiredFlagArg("expect_sha256", ArgumentString, "--expect-sha256", "exact sha256:<hex> digest of the stored change-set bytes"),
		},
	},

	// Backup and recovery beyond the core snapshot lifecycle.
	{ID: "stackkit.backup.configure", ToolName: "stackkit_backup_configure", Title: "Configure backup repository", Description: "Configure the CUE-governed local Kopia repository from the generated backup policy.", Command: []string{"backup", "configure", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{
		ID: "stackkit.backup.restore.activate", ToolName: "stackkit_backup_restore_activate", Title: "Activate restore",
		Description: "Activate one verified staged restore into the live volumes behind an automatic safety snapshot.",
		Command:     []string{"backup", "restore", "activate", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			positionalArg("restore_result_id", "exact staged restore result ID"),
			flagArg("operation_id", ArgumentString, "--operation-id", "stable idempotency key for this activation"),
		},
	},
	{
		ID: "stackkit.backup.restore.recover", ToolName: "stackkit_backup_restore_recover", Title: "Recover restore",
		Description: "Roll back an interrupted restore activation or finish its committed result.",
		Command:     []string{"backup", "restore", "recover", "--json", "--owner-approve", "--rollback"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("activation_operation_id", "exact interrupted activation operation ID")},
	},
	{
		ID: "stackkit.backup.emergency-export", ToolName: "stackkit_backup_emergency_export", Title: "Emergency export",
		Description: "Export local data as a portable archive encrypted to age recipient public keys into a new directory.",
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
		Description: "Decrypt and verify a portable emergency archive into a new staging directory without activating it.",
		Command:     []string{"backup", "emergency-restore", "--json"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("archive", ArgumentString, "--archive", "path to the encrypted emergency archive"),
			requiredFlagArg("identity_file", ArgumentString, "--identity-file", "path to the local age identity file; the key itself never crosses MCP"),
			requiredFlagArg("target", ArgumentString, "--target", "new isolated restore directory whose parent exists"),
			flagArg("max_bytes", ArgumentInteger, "--max-bytes", "maximum decompressed archive bytes"),
		},
	},
	{ID: "stackkit.backup.target.status", ToolName: "stackkit_backup_target_status", Title: "Backup target status", Description: "Verify local backup target custody without contacting S3; returns opaque references only.", Command: []string{"backup", "target", "status"}, Idempotent: true},

	// Break-glass recovery material, referenced by path only.
	{
		ID: "stackkit.break-glass.list", ToolName: "stackkit_break_glass_list", Title: "List break-glass bundles",
		Description: "List the encrypted break-glass bundles on this host.",
		Command:     []string{"break-glass", "list"}, Idempotent: true,
		Arguments: []Argument{flagArg("dir", ArgumentString, "--dir", "break-glass bundle directory")},
	},
	{
		ID: "stackkit.break-glass.show-bundle", ToolName: "stackkit_break_glass_show_bundle", Title: "Show break-glass bundle",
		Description: "Return the path of one node's still-encrypted break-glass bundle.",
		Command:     []string{"break-glass", "show-bundle"}, Idempotent: true,
		Arguments: []Argument{
			positionalArg("node", "node name"),
			flagArg("dir", ArgumentString, "--dir", "break-glass bundle directory"),
		},
	},

	// Host contract.
	{ID: "stackkit.compat", ToolName: "stackkit_compat", Title: "Compatibility diagnostics", Description: "Show published OS support evidence and run non-destructive local container-host diagnostics.", Command: []string{"compat"}, Idempotent: true},
	{ID: "stackkit.prepare", ToolName: "stackkit_prepare", Title: "Prepare check", Description: "Run the read-only native host preflight that Apply uses on this host.", Command: []string{"prepare", "--json", "--non-interactive"}, Idempotent: true},
	{
		ID: "stackkit.host.attach-conformance", ToolName: "stackkit_host_attach_conformance", Title: "Attach host conformance",
		Description: "Attach exact host binding and conformance evidence to Inventory before plan generation.",
		Command:     []string{"host", "attach-conformance"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("inventory", ArgumentString, "--inventory", "workspace-confined inventory.json to update"),
			requiredFlagArg("binding_file", ArgumentString, "--binding", "workspace-confined ExternalHostBinding JSON"),
			requiredFlagArg("receipt_file", ArgumentString, "--receipt", "workspace-confined HostConformanceReceipt JSON"),
		},
	},
	{
		ID: "stackkit.host.conformance", ToolName: "stackkit_host_conformance", Title: "Host conformance receipt",
		Description: "Produce a provider-neutral host conformance receipt, on stdout or as a workspace file.",
		Command:     []string{"host", "conformance"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("binding_file", ArgumentString, "--binding", "local ExternalHostBinding JSON"),
			flagArg("output", ArgumentString, "--output", "receipt destination; omit for JSON on stdout"),
		},
	},
	{ID: "stackkit.host.environment", ToolName: "stackkit_host_environment", Title: "Host environment", Description: "Observe whether this host looks like a home network or a public server.", Command: []string{"host", "environment", "--json"}, Idempotent: true},
	{
		ID: "stackkit.host.preflight", ToolName: "stackkit_host_preflight", Title: "Host preflight",
		Description: "Check whether this host can run the selected StackKit.",
		Command:     []string{"host", "preflight", "--json"}, Idempotent: true,
		Arguments: []Argument{
			flagArg("resolved_plan", ArgumentString, "--resolved-plan", "verified canonical ResolvedPlan whose host listeners to check"),
			flagArg("local_node", ArgumentString, "--local-node", "exact local Node in the supplied ResolvedPlan"),
			enumFlagArg("policy", "--policy", "admission policy", "strict", "warn", "skip"),
		},
	},
	{ID: "stackkit.host.prepare", ToolName: "stackkit_host_prepare", Title: "Prepare Cloud host", Description: "Provision the Cloud Kit execution-channel account, its privileges and workspace-custodied SSH key before apply.", Command: []string{"host", "prepare"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
	{
		ID: "stackkit.host.remediate", ToolName: "stackkit_host_remediate", Title: "Remediate host",
		Description: "Show the reversible fixes for host preflight findings, or carry out one named fix or every unattended reversible fix.",
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
		Description: "Checkpoint, then generate, apply and verify through the governed local lifecycle with automatic verified rollback.",
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
		Description: "Validate, Owner-sign and atomically adopt an external Federation-link binding.",
		Command:     []string{"federation", "binding", "adopt"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("binding_file", ArgumentString, "--binding", "external binding JSON"),
			flagArg("inventory", ArgumentString, "--inventory", "local Inventory path"),
			flagArg("resolved_plan", ArgumentString, "--resolved-plan", "local canonical ResolvedPlan path"),
		},
	},
	{
		ID: "stackkit.federation.binding.import", ToolName: "stackkit_federation_binding_import", Title: "Import Federation binding",
		Description: "Admit an Owner-signed opaque Federation binding into local Inventory.",
		Command:     []string{"federation", "binding", "import"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("admission_file", ArgumentString, "--admission", "Owner-signed binding admission JSON"),
			flagArg("inventory", ArgumentString, "--inventory", "local Inventory path"),
			flagArg("resolved_plan", ArgumentString, "--resolved-plan", "local canonical ResolvedPlan path"),
		},
	},
	{
		ID: "stackkit.federation.control.bind", ToolName: "stackkit_federation_control_bind", Title: "Bind Federation control receiver",
		Description: "Admit exact public Home authority and local receiver custody, replacing the previous receiver authority.",
		Command:     []string{"federation", "control", "bind"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "Owner-reviewed receiver custody JSON without private keys")},
	},
	{
		ID: "stackkit.federation.control.sign", ToolName: "stackkit_federation_control_sign", Title: "Sign Federation control action",
		Description: "Sign one exact short-lived plan or verify action with current Home custody.",
		Command:     []string{"federation", "control", "sign"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "exact action JSON with plan hash, target and bounded timestamps")},
	},
	{
		ID: "stackkit.federation.control.send", ToolName: "stackkit_federation_control_send", Title: "Send Federation control action",
		Description: "Send one Home-signed action to the selected Cloud node over an exact Home-initiated mTLS connection.",
		Command:     []string{"federation", "control", "send"}, Mutation: true, OpenWorld: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("file", ArgumentString, "--file", "Home-signed action JSON"),
			requiredFlagArg("endpoint_file", ArgumentString, "--endpoint-file", "local pinned mTLS endpoint and confined credential paths"),
		},
	},
	{ID: "stackkit.federation.control.withdraw", ToolName: "stackkit_federation_control_withdraw", Title: "Withdraw Federation control receiver", Description: "Persistently withdraw receiver authority, including existing TLS sessions.", Command: []string{"federation", "control", "withdraw"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
	{
		ID: "stackkit.federation.link.bind", ToolName: "stackkit_federation_link_bind", Title: "Bind Federation link",
		Description: "Adopt external WireGuard handles into local Owner custody.",
		Command:     []string{"federation", "link", "bind"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "external fabric custody JSON without private keys")},
	},
	{
		ID: "stackkit.federation.link.stop", ToolName: "stackkit_federation_link_stop", Title: "Stop Federation link",
		Description: "Stop the adopted Federation interface without deleting the external fabric.",
		Command:     []string{"federation", "link", "stop"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("fabric_ref", ArgumentString, "--fabric-ref", "exact adopted opaque fabric reference")},
	},

	// Local identity plane.
	{
		ID: "stackkit.identity.projection.inspect", ToolName: "stackkit_identity_projection_inspect", Title: "Inspect identity projection",
		Description: "Verify one signed, credential-free identity projection without mutation.",
		Command:     []string{"identity", "projection", "inspect", "--json"}, Idempotent: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "signed projection JSON")},
	},
	{
		ID: "stackkit.identity.projection.approve", ToolName: "stackkit_identity_projection_approve", Title: "Approve identity projection",
		Description: "Create the local Owner-signed approval of one projection without changing PocketID.",
		Command:     []string{"identity", "projection", "approve", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("file", ArgumentString, "--file", "signed projection JSON")},
	},
	{
		ID: "stackkit.identity.projection.apply", ToolName: "stackkit_identity_projection_apply", Title: "Apply identity projection",
		Description: "Apply one previously approved identity projection to local PocketID.",
		Command:     []string{"identity", "projection", "apply", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("projection_sha256", ArgumentString, "--projection-sha256", "exact approved projection digest")},
	},
	{
		ID: "stackkit.identity.projection.unlink", ToolName: "stackkit_identity_projection_unlink", Title: "Unlink identity projection",
		Description: "Detach optional identity sync without deleting any local identity.",
		Command:     []string{"identity", "projection", "unlink", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("projection_sha256", ArgumentString, "--projection-sha256", "exact linked projection digest")},
	},
	{
		ID: "stackkit.identity.workload-peer.request", ToolName: "stackkit_identity_workload_peer_request", Title: "Request workload peer",
		Description: "Create or reuse a locally held workload key and return only its public CSR.",
		Command:     []string{"identity", "workload-peer", "request", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity"),
			flagArg("rotate_key", ArgumentBoolean, "--rotate-key", "generate a new key while preserving the active credential until install"),
		},
	},
	{
		ID: "stackkit.identity.workload-peer.enroll", ToolName: "stackkit_identity_workload_peer_enroll", Title: "Enroll workload peer",
		Description: "Issue and admit a workload peer for one installed Home publication; returns only public certificate documents.",
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
		Description: "Install a Home-issued workload certificate using an independently verified Home root.",
		Command:     []string{"identity", "workload-peer", "install", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("file", ArgumentString, "--file", "public peer credential JSON"),
			requiredFlagArg("home_root_sha256", ArgumentString, "--home-root-sha256", "Home root sha256 fingerprint verified through a separate Owner-approved channel"),
		},
	},
	{
		ID: "stackkit.identity.workload-peer.revoke", ToolName: "stackkit_identity_workload_peer_revoke", Title: "Revoke workload peer",
		Description: "Withdraw a workload peer's current Home admission.",
		Command:     []string{"identity", "workload-peer", "revoke", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity")},
	},
	{
		ID: "stackkit.identity.workload-peer.probe", ToolName: "stackkit_identity_workload_peer_probe", Title: "Probe workload peer",
		Description: "Request the workload origin through an existing loopback Federation socket.",
		Command:     []string{"identity", "workload-peer", "probe"}, Idempotent: true,
		Arguments: []Argument{
			requiredFlagArg("peer_ref", ArgumentString, "--peer-ref", "explicit workload peer identity"),
			requiredFlagArg("address", ArgumentString, "--address", "existing loopback Federation socket address"),
		},
	},

	// Releases, logs and migration.
	{
		ID: "stackkit.kit.list", ToolName: "stackkit_kit_list", Title: "List releases",
		Description: "List published StackKits releases from the public release index.",
		Command:     []string{"kit", "list", "--json"}, Idempotent: true, OpenWorld: true,
		Arguments: []Argument{enumFlagArg("channel", "--channel", "release channel", "stable", "beta", "edge")},
	},
	{
		ID: "stackkit.logs.read", ToolName: "stackkit_logs_read", Title: "Read logs",
		Description: "Read one structured rollout log page, the newest by default.",
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
		Description: "Classify a StackSpec v1, emit its migration report and optionally write the completed canonical StackSpec v2 to a new path.",
		Command:     []string{"migrate"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			optionalPositionalArg("v1_spec_file", "StackSpec v1 path; defaults to the workspace spec"),
			flagArg("target_kit", ArgumentString, "--target-kit", "explicit target KitProfile"),
			flagArg("complete_with", ArgumentString, "--complete-with", "full explicit StackSpec v2 candidate path"),
			flagArg("output", ArgumentString, "--output", "write the migration result to this new workspace path instead of stdout"),
			flagArg("spec_output", ArgumentString, "--spec-output", "write the completed canonical StackSpec v2 to this new workspace path"),
			enumFlagArg("format", "--format", "output format", "json", "yaml"),
		},
	},

	// Module authoring.
	{
		ID: "stackkit.module.lint", ToolName: "stackkit_module_lint", Title: "Lint modules",
		Description: "Lint local module CUE for pin, health, security, access and placement hygiene.",
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
		Description: "Render module artifacts deterministically from module_facts.json.",
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
		Description: "Inspect, or explicitly garbage-collect, one retired Architecture v2 output-transaction tombstone.",
		Command:     []string{"output-transaction", "gc-retired"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{
			requiredFlagArg("transaction_id", ArgumentString, "--transaction-id", "exact lowercase transaction ID"),
			flagArg("action", ArgumentString, "--action", "exact action reported by a prior inspection; required with apply"),
			flagArg("apply", ArgumentBoolean, "--apply", "execute the echoed action; inspection is the default"),
		},
	},
	{ID: "stackkit.registry.info", ToolName: "stackkit_registry_info", Title: "Registry info", Description: "Summarize the embedded StackKits registry snapshot.", Command: []string{"registry", "info"}, Idempotent: true},

	// Secret custody, without secret values.
	{ID: "stackkit.secrets.materialize", ToolName: "stackkit_secrets_materialize", Title: "Materialize secret custody", Description: "Establish or reuse owner-bound local custody for every secret the current StackSpec declares; never returns values.", Command: []string{"secrets", "materialize"}, Mutation: true, Idempotent: true, OwnerApproval: true},

	// Plan-declared services.
	{
		ID: "stackkit.service.logs", ToolName: "stackkit_service_logs", Title: "Service logs",
		Description: "Read bounded, redacted logs of one managed service.",
		Command:     []string{"service", "logs", "--json"}, Idempotent: true,
		Arguments: []Argument{
			positionalArg("service_key", "managed service key"),
			flagArg("tail", ArgumentInteger, "--tail", "number of redacted log entries (1..200)"),
			flagArg("cursor", ArgumentString, "--cursor", "opaque cursor from a previous page"),
		},
	},
	{
		ID: "stackkit.service.start", ToolName: "stackkit_service_start", Title: "Start service",
		Description: "Start one managed service declared by the active plan.",
		Command:     []string{"service", "start", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("service_key", "managed service key")},
	},
	{
		ID: "stackkit.service.stop", ToolName: "stackkit_service_stop", Title: "Stop service",
		Description: "Stop one managed, non-critical service declared by the active plan.",
		Command:     []string{"service", "stop", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("service_key", "managed service key")},
	},
	{
		ID: "stackkit.service.restart", ToolName: "stackkit_service_restart", Title: "Restart service",
		Description: "Restart one managed service declared by the active plan.",
		Command:     []string{"service", "restart", "--json", "--owner-approve"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("service_key", "managed service key")},
	},

	// Support evidence.
	{
		ID: "stackkit.support.export", ToolName: "stackkit_support_export", Title: "Export support evidence",
		Description: "Export one redacted local rollout log and its receipts to a new local file.",
		Command:     []string{"support", "export"}, Mutation: true, OwnerApproval: true,
		Arguments: []Argument{
			optionalPositionalArg("run_id", "run ID; omit for the newest log"),
			requiredFlagArg("output", ArgumentString, "--output", "new local JSON file to create"),
		},
	},

	// Household users in local PocketID. Setup and activation URLs never cross MCP.
	{ID: "stackkit.user.list", ToolName: "stackkit_user_list", Title: "List users", Description: "List household users in local PocketID.", Command: []string{"user", "list", "--json"}, Idempotent: true},
	{ID: "stackkit.user.owner.status", ToolName: "stackkit_user_owner_status", Title: "Owner activation status", Description: "Read the owner passkey activation status without its activation URL.", Command: []string{"user", "owner", "status", "--json"}, Idempotent: true},
	{
		ID: "stackkit.user.remove", ToolName: "stackkit_user_remove", Title: "Remove user",
		Description: "Remove one household user from local PocketID; owner and admin identities are refused.",
		Command:     []string{"user", "remove", "--json", "--owner-approve"}, Mutation: true, Destructive: true, OwnerApproval: true,
		Arguments: []Argument{positionalArg("username", "household username")},
	},
}

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
