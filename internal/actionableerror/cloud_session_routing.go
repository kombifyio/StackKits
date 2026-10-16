package actionableerror

func cloudSessionDiagnostics() []Contract {
	rows := []struct {
		reason, cause string
		retry         bool
	}{
		{"cloud_session_tinyauth_pin_unsupported", "The retained TinyAuth image differs from the component pin qualified for the current static authorization policy.", false},
		{"cloud_session_transition_invalid", "The signed Cloud session transition or its canonical route projection is invalid.", false},
		{"cloud_session_transition_drift", "The retained Cloud configuration differs from its signed session transition.", false},
		{"cloud_session_transition_pending", "The Cloud session route transition has not reached its verified active phase.", true},
		{"cloud_session_transition_write_failed", "The private Cloud session transition journal was not durably written.", true},
		{"cloud_session_transition_unsupported", "An installed non-core workload has no qualified session route migration.", false},
		{"cloud_session_runtime_pending", "Reconciliation of the two Cloud session consumers was interrupted.", true},
		{"cloud_session_runtime_unavailable", "The retained Cloud services or their exact Compose configuration cannot be observed.", true},
		{"cloud_session_client_unavailable", "The existing PocketID TinyAuth client cannot be read or updated.", true},
		{"cloud_session_client_drift", "The existing TinyAuth client differs from the callback-only transition policy.", false},
		{"cloud_session_callback_pending", "The callback update has an uncertain result or its readback is unavailable.", true},
		{"cloud_session_owner_drift", "The established Cloud Owner identity differs from its retained binding.", false},
		{"cloud_session_passkey_required", "The existing Cloud Owner has no enrolled passkey to retain.", false},
		{"cloud_session_passkey_drift", "The Cloud Owner passkey set changed during the session transition.", false},
		{"cloud_session_upgrade_unsupported", "This Cloud session transition retains its original release artifact and cannot accept another release artifact.", false},
	}
	result := make([]Contract, 0, len(rows))
	for _, row := range rows {
		next := "Retain the original workspace, custody, client, enrolled passkeys and pinned images. Inspect the named condition, then repeat the interrupted `stackkit address session-zone --zone <original-allocated-zone>` invocation, retaining `--abort` for abort recovery. Resume reads back uncertain effects before continuing; never rewrite seals, reset identities, broaden cookies or bypass TLS."
		if row.reason == "cloud_session_upgrade_unsupported" {
			next = "Keep the retained source release and active session transition. Cloud release upgrades remain unsupported. Export redacted evidence with `stackkit support export` and follow docs/runbooks/kit-upgrade.md; a receipt-qualified managed migration is required before changing the retained artifact."
		}
		if row.reason == "cloud_session_tinyauth_pin_unsupported" {
			next = "Keep the original workspace, custody and enrolled passkeys. For the qualified retained TinyAuth 5.0.7 image and canonical node-local database, inspect `stackkit address session-zone --zone <allocated-zone> --check --migrate-tinyauth`, then repeat without --check. Other pins or database configurations require their own qualified migration. Cloud release upgrades remain unsupported; CLI publication uses the release train. To recover an interrupted transition, repeat its original command or restore its signed source with --abort; recovery retains both original and newer data."
		}
		result = append(result, New("stackkit_command_failed", row.reason, row.cause, []string{next}, row.retry))
	}
	return result
}

func allLifecycleDiagnostics() []Contract {
	return append(append([]Contract(nil), lifecycleDiagnostics...), cloudSessionDiagnostics()...)
}
