package actionableerror

import (
	"sort"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
)

// The runtime taxonomy owns runtime causes and actions. This authority adds
// construction-owned lifecycle conditions; CLI, MCP and generated docs consume
// the same contracts rather than keeping their own recovery text.
var lifecycleDiagnostics = []Contract{
	New("stackkit_command_failed", "identity_cookie_scope_requires_install_zone",
		"The shared kombify.me parent cannot carry a tenant-isolated login cookie across this installation's service hosts.",
		[]string{"Initialize with your own domain or an allocated install zone, for example `--domain <zone>.kombify.me`. Keep existing Cloud custody and RP-bound passkeys: its origin is immutable, so use a clean owned installation for the chosen domain instead of editing or resealing it. Never broaden cookie scope to `.kombify.me`."}, false),
	New("stackkit_command_failed", "pocketid_tinyauth_reconcile_owner_approval_required",
		"PKCE-only identity convergence requires explicit Owner approval.",
		[]string{"Keep the existing workspace and custody. Use `stackkit user owner reconcile --owner-approve --json` only in the authorized Owner context; managed servers use authenticated Techstack server access. This action cannot replace identities, credentials, grants or enrollment links."}, false),
	New("stackkit_command_failed", "pocketid_tinyauth_binding_mismatch",
		"The PocketID TinyAuth client policy differs from verified local Owner custody.",
		[]string{"Retain the original workspace and custody. For PKCE-only drift use `stackkit user owner reconcile --owner-approve --json` through authorized Techstack server access on a managed server, or the local Owner context on a standalone server. It preserves credentials, enrollment and grants and refuses other drift. Callback, client or group drift requires the owning lifecycle's binding reconciliation; do not reset the owner, issue activation links or overwrite grants to bypass verification."}, false),
	New("stackkit_command_failed", "pocketid_tinyauth_client_unreadable",
		"The authenticated PocketID TinyAuth client readback failed; policy drift is not established.",
		[]string{"Inspect local PocketID health and authenticated API availability, then retry owner status with the original workspace and custody. Do not recreate the client, rotate credentials or infer missing passkeys from a failed read."}, true),
	New("stackkit_command_failed", "client_trust_approval_required",
		"Installing or removing this client trust profile requires explicit local OS approval.",
		[]string{"Review the Owner-CA fingerprint, then run `sudo sh enroll-linux.sh --approve` from the client's user session; use `--remove` to undo only this profile."}, false),
	New("stackkit_command_failed", "client_trust_browser_user_missing",
		"The approving Linux user's browser trust store is unavailable.",
		[]string{"Run sudo from the intended non-root Linux user's session; do not enroll the root user's browser or substitute another user's trust store."}, false),
	New("stackkit_command_failed", "client_trust_adapter_unavailable",
		"This client does not provide the supported Linux NetworkManager trust adapter dependencies.",
		[]string{"Use a Debian/Ubuntu client managed by NetworkManager with dnsmasq, ca-certificates, openssl and libnss3-tools installed, then retry the public profile; other OS adapters remain unverified."}, false),
	New("stackkit_command_failed", "client_trust_dns_upstream_unsupported",
		"The NetworkManager dnsmasq adapter cannot preserve a connection's loopback DNS upstream.",
		[]string{"Retain the existing resolver settings. This adapter requires the connection's reachable network DNS upstream; use it only after normal name resolution is configured and verified. No DNS or CA profile was installed."}, false),
	New("stackkit_command_failed", "client_trust_ca_mismatch",
		"The profile's public CA differs from its approved Owner-CA fingerprint.",
		[]string{"Obtain the public profile again through trusted server access and compare the installed host's fingerprint; never import a substituted root or bypass TLS."}, false),
	New("stackkit_command_failed", "client_trust_local_conflict",
		"Local DNS or CA trust changed outside this profile's ownership.",
		[]string{"Preserve the reported local path or user trust, resolve the ownership conflict, then retry; do not overwrite unrelated network configuration or CA roots."}, false),
	New("stackkit_command_failed", "client_trust_profile_missing",
		"This public trust profile has no local client installation record.",
		[]string{"Use the profile originally approved on this client; do not remove another profile's DNS or CA trust."}, false),
	New("stackkit_command_failed", "client_trust_profile_invalid",
		"The public client trust handoff does not match authenticated private-zone Owner custody.",
		[]string{"Keep the original workspace and signed CA/resolver custody; restore them or finish the supported private-domain migration, then retry `stackkit verify client-trust --export`. Never substitute a CA or relax Plan verification."}, false),
	New("stackkit_command_failed", "client_trust_browser_ca_unsupported",
		"The installed legacy Ed25519 CA chain is rejected by the browser certificate verifier.",
		[]string{"Keep existing Owner custody and application data. Fresh installations use the current CLI's P-256 CA; an existing CA needs an explicit governed migration. Export redacted evidence with `stackkit support export --output support-evidence.json` and do not delete custody, rotate a CA implicitly or bypass TLS."}, false),
	New("stackkit_command_failed", "origin_tls_rejected",
		"The public edge rejected origin TLS with HTTP 526.",
		[]string{"Inspect the origin certificate and exact route hostname; managed kombify.me uses its delivered Origin CA certificate, while Standard/custom domains use their declared issuer. Keep strict TLS verification."}, true),
	New("stackkit_command_failed", "pki_leaf_unavailable",
		"The declared internal TLS leaf is not available through its route yet.",
		[]string{"Keep the existing CA custody, inspect the router and step-ca health, then retry Apply; do not bypass TLS verification."}, true),
	New("stackkit_command_failed", "namespace_probe_unverified",
		"The namespace probe could not verify the target kernel's container support.",
		[]string{"On the target Linux host, run `stackkit host preflight` with non-interactive sudo or as root; an unprivileged unshare refusal does not prove an incompatible kernel."}, true),
	New("stackkit_command_failed", "runtime-capacity-unsatisfied",
		"The observed host capacity does not satisfy the selected module profiles.",
		[]string{"Inspect the attested host inventory and selected profiles, then resize the host or reduce demand; nominal RAM includes the kernel reservation and must not be guessed from rounded MemTotal."}, true),
	New("stackkit_command_failed", "host_virtualization_unrecognized",
		"The observed virtualization label is not recognized by this CLI.",
		[]string{"Use the current released CLI and regenerate the measured inventory; EC2 Nitro's systemd `amazon` label is recognized as KVM. Do not substitute an invented inventory fact."}, true),
	New("stackkit_command_failed", "authorization_time_mismatch",
		"A runtime child differs from its sealed invocation authorization instant.",
		[]string{"Use the current released CLI, regenerate and retry the same Apply; retain the node's offsite backup-target binding and do not override authorization time."}, true),
	New("stackkit_command_failed", "workspace_missing",
		"No StackKit specification is available in the selected workspace.",
		[]string{"Select the original workspace with `stackkit -C <workspace>` or rerun the original installer with its saved workspace; initialize only a new installation."}, false),
	New("stackkit_command_failed", "binding_mismatch",
		"The saved generation binding differs from the current host or specification.",
		[]string{"In the original workspace, rerun the original installer or `stackkit generate` before Apply; retain the Owner custody, saved choices and installation journal."}, true),
	New("stackkit_command_failed", "restore_original_release_required",
		"Interrupted restore recovery requires the original admitted release and Owner journal.",
		[]string{"Use `stackkit backup restore recover --help` for original-release recovery with the exact release, archive/index digests and original Plan hash; retain the signed journal and never force activation with substituted evidence."}, false),
	New("stackkit_command_failed", "stored_plan_invalid",
		"The stored ResolvedPlan is not accepted by this CLI's canonical Plan contract.",
		[]string{"Keep the original Plan, manifest, release and Owner custody unchanged. Inspect the retained generation with `stackkit plan --source-generation --json` through its already installed attested source release; if unavailable, use `stackkit kit prepare-source-generation --help`. Follow the kit's supported upgrade policy; do not edit the Plan, replay the installer or use a newly resolved candidate as execution authority."}, false),
	New("stackkit_command_failed", "upgrade_kit_unsupported",
		"The selected StackKit's CUE policy does not support a fresh standalone upgrade.",
		[]string{"Keep the installed release and its original workspace; export redacted evidence with `stackkit support export --output support-evidence.json`. Fresh upgrade is available only through the Basement preview transaction described in docs/runbooks/kit-upgrade.md. An existing interrupted transaction uses `stackkit upgrade --recover <operation-id>` with its original admitted release and journal; reinstall or remove is not an upgrade route."}, false),
	New("stackkit_command_failed", "remove_incomplete",
		"Some local StackKits resources could not be removed; the workspace recovery state has been retained.",
		[]string{"Inspect the reported local resource and Docker availability; preserve the original workspace, release and backups. Release a blocking resource only within its owner-approved scope, then rerun remove with the same data policy. --force does not bypass incomplete removal or erase recovery custody."}, true),
	New("stackkit_command_failed", "unknown_failure",
		"The operation failed without a recognized terminal diagnosis.",
		[]string{"Inspect `stackkit logs latest --json` and `stackkit support export --help`; retain the original workspace and report the bounded redacted evidence."}, false),
}

type RecordedFailure struct {
	Failure string   `json:"failure"`
	Reasons []string `json:"reasonCodes"`
}

// RecordedFailures indexes the historical terminal conditions to the same
// contracts; consumers never maintain a separate copy of this mapping.
func RecordedFailures() []RecordedFailure {
	return []RecordedFailure{
		{"ACME duplicate-certificate limit", []string{"acme_rate_limited"}},
		{"Managed origin HTTP 526 / kombify.me certificate wait", []string{"origin_tls_rejected", "origin_certificate_missing"}},
		{"Kopia OOM", []string{"oom_killed"}},
		{"PKI leaf before route", []string{"pki_leaf_unavailable"}},
		{"Unprivileged unshare probe", []string{"namespace_probe_unverified"}},
		{"Nominal RAM / MemTotal admission", []string{"runtime-capacity-unsatisfied"}},
		{"EC2 Nitro virtualization label", []string{"host_virtualization_unrecognized"}},
		{"Offsite backup child/router authorization", []string{"authorization_time_mismatch"}},
		{"Interrupted Cloud workspace rerun", []string{"workspace_missing", "binding_mismatch"}},
		{"Interrupted restore after CLI upgrade", []string{"restore_original_release_required"}},
		{"Stored generation rejected by a newer CLI", []string{"stored_plan_invalid"}},
		{"Fresh Cloud/Modern standalone upgrade", []string{"upgrade_kit_unsupported"}},
		{"Interrupted whole-deployment removal", []string{"remove_incomplete"}},
	}
}

// Diagnostic is a closed lookup, never a classification of arbitrary reason
// text. RetryAfter remains supplied by the observed external authority.
func Diagnostic(reason string) (Contract, bool) {
	for _, entry := range allLifecycleDiagnostics() {
		if entry.ReasonCode == reason {
			entry.UserGuidance = append([]string(nil), entry.UserGuidance...)
			return entry, true
		}
	}
	class := applyoutcome.Class(reason)
	guidance := applyoutcome.Remediation(class)
	if len(guidance) < 2 {
		return Contract{}, false
	}
	return New("stackkit_command_failed", reason, guidance[0], guidance[1:], applyoutcome.Retryable(class)), true
}

// Diagnostics is the deterministic projection used by the self-check plan and
// troubleshooting generator. A catalog entry is guidance, not measured health.
func Diagnostics() []Contract {
	result := make([]Contract, 0, len(lifecycleDiagnostics)+len(applyoutcome.Classes()))
	for _, entry := range allLifecycleDiagnostics() {
		entry.UserGuidance = append([]string(nil), entry.UserGuidance...)
		result = append(result, entry)
	}
	for _, class := range applyoutcome.Classes() {
		if entry, ok := Diagnostic(string(class)); ok {
			result = append(result, entry)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ReasonCode < result[j].ReasonCode })
	return result
}
