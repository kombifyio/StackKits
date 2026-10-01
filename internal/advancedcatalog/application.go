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
	return operations
}
