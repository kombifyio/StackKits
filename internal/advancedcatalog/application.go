package advancedcatalog

func applicationOperations() []Operation {
	operations := []Operation{}
	for _, verb := range []string{"inspect", "adopt", "verify", "release", "control"} {
		mutates := verb == "adopt" || verb == "release" || verb == "control"
		argv := []string{"application", verb, "--workload", "{workloadRef}", "--container-id", "{containerId}", "--json"}
		if mutates {
			argv = append(argv, "--operation-id", "{operationId}", "--owner-approve")
		}
		if verb == "adopt" {
			argv = append(argv, "--plan-digest", "{planDigest}")
		}
		if verb == "control" {
			argv = append(argv, "--action", "{applicationAction}")
		}
		optional := []OptionalArgs{}
		if verb == "inspect" || verb == "adopt" || verb == "verify" {
			optional = append(optional, OptionalArgs{Argv: []string{"--owner-file", "{ownerFile}"}, Description: "Private existing native Owner grant file; secret values never cross the dispatch contract."})
		}
		operations = append(operations, Operation{Operation: "application." + verb, Status: StatusAvailable, SinceRelease: SincePending, Summary: "Source-bound supported application " + verb + " through local Owner custody; no recreation or account bootstrap.", Command: "stackkit application " + verb, Argv: argv, OptionalArgs: optional, Mutates: mutates, Requires: Requirements{OwnerApproval: mutates}, Inputs: []Input{}, Results: []Outcome{{Status: "success", ContractRef: ref("stackkit.application-adoption/v1", "schemas/stackkit-application-adoption-v1.schema.json"), Description: "Source and authority-bound adoption evidence."}}, Events: []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed}})
	}
	for _, verify := range []bool{false, true} {
		name := "application.connector"
		argv := []string{"setup", "connector", "--workload", "smart-home", "--request-file", "{connectorRequestFile}", "--operation-id", "{operationId}", "--json"}
		if verify {
			name += ".verify"
			argv = append(argv, "--verify-only")
		} else {
			argv = append(argv, "--owner-approve")
		}
		operations = append(operations, Operation{Operation: name, Status: StatusAvailable, SinceRelease: SincePending, Summary: "Exact local Home Assistant connector setup or read-only signed receipt reconciliation; credential custody remains on the node.", Command: "stackkit setup connector", Argv: argv, OptionalArgs: []OptionalArgs{}, Mutates: !verify, Requires: Requirements{OwnerApproval: !verify}, Inputs: []Input{{Placeholder: "connectorRequestFile", ContractRef: ref("stackkit.home-assistant-connector-request/v1", "schemas/stackkit-home-assistant-connector-request-v1.schema.json"), Description: "Closed ensure/revoke request; external resource and revision are correlation only."}}, Results: []Outcome{{Status: "success", ContractRef: ref("stackkit.home-assistant-connector-result/v1", "schemas/stackkit-home-assistant-connector-result-v1.schema.json"), Description: "Exact signed setup receipt bytes verified against current node custody and local deployment."}}, Events: []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed}})
	}
	operations = append(operations, Operation{Operation: "application.home-graph.sync", Status: StatusAvailable, SinceRelease: SincePending, Summary: "Project the household home graph onto the exact local Home Assistant (kombify leading) or read its floors and areas (Home Assistant leading); the credential stays on the node, nothing is deleted in Home Assistant. Dispatched without a fresh owner approval: the owner's edit of the home graph in kombify is the approval.", Command: "stackkit setup home-graph", Argv: []string{"setup", "home-graph", "--workload", "smart-home", "--request-file", "{connectorRequestFile}", "--operation-id", "{operationId}", "--graph-file", "{homeGraphFile}", "--json"}, OptionalArgs: []OptionalArgs{}, Mutates: true, Requires: Requirements{OwnerApproval: false}, Inputs: []Input{{Placeholder: "connectorRequestFile", ContractRef: ref("stackkit.home-assistant-connector-request/v1", "schemas/stackkit-home-assistant-connector-request-v1.schema.json"), Description: "The ensure request of the active connector; external resource and revision are correlation only."}, {Placeholder: "homeGraphFile", ContractRef: ref("stackkit.home-graph-sync-request/v1", "schemas/stackkit-home-graph-sync-request-v1.schema.json"), Description: "The kombify.home_graph.v1 document plus the drift items the owner chose to restore; at most 512 KiB."}}, Results: []Outcome{{Status: "success", ContractRef: ref("stackkit.home-graph-sync-result/v1", "schemas/stackkit-home-graph-sync-result-v1.schema.json"), Description: "Links, drift and a bounded Home Assistant floor and area inventory; devices are only counted."}}, Events: []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed}})
	return append(operations, applicationStackOperations()...)
}

// applicationStackOperations are "Stack it": one existing single container
// StackKits did not install, rebuilt as an own application
// (techstack.own-application/v4) with the own-application lifecycle.
func applicationStackOperations() []Operation {
	result := []Outcome{{Status: "success", ContractRef: ref("stackkit.own-application-adoption/v1", "schemas/stackkit-own-application-adoption-v1.schema.json"), Description: "Rows kept, changed or refused, the plan digest, the outcome code and, after adoption, the own-application result and retention deadline; environment values never appear."}}
	base := []string{"app", "own", "--from-container", "{containerId}", "--name", "{ownApplicationName}"}
	planOptions := []OptionalArgs{
		{Argv: []string{"--no-outbound"}, Description: "Adopt without outbound Internet; published host ports stay and only answer connections made to them."},
		{Argv: []string{"--http-port", "{httpPort}"}, Description: "The web port when the container exposes several."},
		{Argv: []string{"--secret-env", "{environmentName}"}, Description: "Also classify this variable as a credential (repeatable); every value is hidden anyway."},
	}
	entry := func(operation, summary string, argv []string, optional []OptionalArgs, mutates bool) Operation {
		return Operation{Operation: operation, Status: StatusAvailable, SinceRelease: SincePending, Summary: summary, Command: "stackkit app own", Argv: append(append([]string{}, base...), argv...), OptionalArgs: optional, Mutates: mutates, Requires: Requirements{OwnerApproval: mutates}, Inputs: []Input{}, Results: result, Events: []EventPhase{}, Modes: Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed}}
	}
	return []Operation{
		entry("application.stack.plan", "Inspect an existing container and plan its rebuild as an own application: kept, changed and refused settings, recreation and downtime, data copy size against free disk; returns the plan digest. Changes nothing.", []string{"--phase", "plan", "--json"}, planOptions, false),
		entry("application.stack", "Stack it: stop the reviewed original (kept, restart policy no), copy and checksum its one data source, create the own application and hold a 2-minute stability window; any failure removes the new workload and starts the original (outcome rolled_back or rollback_failed).", []string{"--phase", "adopt", "--plan-digest", "{planDigest}", "--operation-id", "{operationId}", "--owner-approve", "--json"}, planOptions, true),
		entry("application.stack.verify", "Read the adoption: checkpoint, signed own-application status, published port answers, restart stability and the retention deadline.", []string{"--phase", "verify", "--json"}, []OptionalArgs{}, false),
		entry("application.stack.restore-original", "Within the 7-day retention window: remove the rebuilt application (its volume retained) and start the original with its original restart policy.", []string{"--restore-original", "--operation-id", "{operationId}", "--owner-approve", "--json"}, []OptionalArgs{}, true),
		entry("application.stack.release-original", "Remove the stopped original after the owner confirms; its volumes and bind sources stay. Never automatic.", []string{"--release-original", "--operation-id", "{operationId}", "--owner-approve", "--json"}, []OptionalArgs{}, true),
	}
}
