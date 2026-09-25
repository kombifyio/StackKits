// Package advancedcatalog is the single source of truth for the Advanced
// operations catalog (stackkit.advanced-operations/v1): every Advanced
// operation an orchestrator such as Techstack may dispatch to the stackkit
// CLI, with its exact argv template, admission requirements, input and result
// contracts, rollout event phases and denial envelope.
//
// The catalog is rendered to docs/data/advanced-operations/latest.json by
// `stackkit docs emit-advanced-operations` and shipped in every release
// archive. The command package tests prove that every available entry's argv
// resolves to a registered command and flags, so the catalog cannot drift
// from the CLI.
package advancedcatalog

import (
	"bytes"
	"encoding/json"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advanceddrift"
)

const (
	SchemaVersion = "stackkit.advanced-operations/v1"
	// DefaultPath is the committed catalog, relative to the repository root
	// and to the root of every release archive.
	DefaultPath = "docs/data/advanced-operations/latest.json"
	// SchemaPath is the JSON Schema of the catalog document.
	SchemaPath = "schemas/stackkit-advanced-operations-v1.schema.json"
	Program    = "stackkit"

	// SincePending marks an entry that no published release carries yet.
	SincePending = "pending"

	StatusAvailable = "available"
	StatusPlanned   = "planned"

	// Non-capability operation IDs. Capability operation IDs are the
	// internal/advancedcapability constants.
	OperationTrustImport         = "advanced.trust.import"
	OperationTrustInspect        = "advanced.trust.inspect"
	OperationDriftDetectAdvanced = "drift.detect.advanced"
)

// Mode admission values.
const (
	AdmissionAllowed       = "allowed"
	AdmissionDenied        = "denied"
	AdmissionCapability    = "capability"
	AdmissionOwnerApproval = "owner-approval"
)

// Contract schema versions and their schema files, relative to the
// repository and release archive root.
const (
	CommandResultSchemaVersion      = "stackkit.command-result/v1"
	CommandResultSchema             = "schemas/stackkit-command-result-v1.schema.json"
	RolloutEventSchemaVersion       = "stackkit.rollout-event/v1"
	RolloutEventSchema              = "schemas/stackkit-rollout-event.schema.json"
	OperationDenialSchemaVersion    = "stackkit.operation-denial/v1"
	OperationDenialSchema           = "schemas/stackkit-operation-denial-v1.schema.json"
	ActionableErrorSchemaVersion    = "stackkit.actionable-error/v1"
	ActionableErrorSchema           = "schemas/stackkit-actionable-error-v1.schema.json"
	TrustBundleSchemaVersion        = advancedcapability.TrustBundleSchemaVersion
	TrustBundleSchema               = "schemas/stackkit-advanced-trust-bundle-v1.schema.json"
	LocalTrustSchemaVersion         = "stackkit.local-advanced-trust/v1"
	LocalTrustSchema                = "schemas/stackkit-local-advanced-trust-v1.schema.json"
	CapabilitySchemaVersion         = advancedcapability.SchemaVersion
	CapabilitySchema                = "schemas/stackkit-advanced-capability-v1.schema.json"
	ChangeSetSchemaVersion          = "stackkit.advanced-change-set/v2"
	ChangeSetRecordSchema           = "schemas/stackkit-advanced-change-set-v2.schema.json"
	ChangeSetCreateResultSchema     = "schemas/stackkit-advanced-change-set-create-result-v2.schema.json"
	AdvancedMutationSchemaVersion   = "stackkit.advanced-mutation/v1"
	AdvancedMutationSchema          = "schemas/stackkit-advanced-mutation-v1.schema.json"
	ChangeSetResultSchemaVersion    = "stackkit.change-set-result/v1"
	ChangeSetResultSchema           = "schemas/stackkit-change-set-result-v1.schema.json"
	DriftReportSchemaVersion        = "stackkit.drift-report/v1"
	DriftReportSchema               = "schemas/stackkit-drift-report-v1.schema.json"
	RestoreDrillReportSchemaVersion = "stackkit.restore-drill-report/v1"
	RestoreDrillReportSchema        = "schemas/stackkit-restore-drill-report-v1.schema.json"
)

// Catalog is the stackkit.advanced-operations/v1 document.
type Catalog struct {
	SchemaVersion string         `json:"schemaVersion"`
	Program       string         `json:"program"`
	Description   string         `json:"description"`
	CommandResult CommandResult  `json:"commandResult"`
	RolloutEvents RolloutEvents  `json:"rolloutEvents"`
	Denial        Denial         `json:"denial"`
	GlobalArgs    []OptionalArgs `json:"globalArgs"`
	Placeholders  []Placeholder  `json:"placeholders"`
	Operations    []Operation    `json:"operations"`
}

// ContractRef names a versioned JSON document and its schema file.
type ContractRef struct {
	SchemaVersion string `json:"schemaVersion"`
	Schema        string `json:"schema"`
}

// CommandResult describes the stdout envelope of every `--json` invocation.
type CommandResult struct {
	ContractRef
	Statuses []string `json:"statuses"`
	// NoEnvelope states what a missing envelope means.
	NoEnvelope string `json:"noEnvelope"`
}

// RolloutEvents describes the JSONL progress stream.
type RolloutEvents struct {
	ContractRef
	Argv        []string `json:"argv"`
	Description string   `json:"description"`
}

// Denial is the structured denial envelope an orchestrator must handle.
type Denial struct {
	ContractRef
	CommandResultStatus string       `json:"commandResultStatus"`
	ReasonCodes         []ReasonCode `json:"reasonCodes"`
}

type ReasonCode struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type Placeholder struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Pattern     string `json:"pattern,omitempty"`
}

// OptionalArgs are argv words an orchestrator may append.
type OptionalArgs struct {
	Argv        []string `json:"argv"`
	Description string   `json:"description"`
}

type Operation struct {
	Operation    string `json:"operation"`
	Status       string `json:"status"`
	SinceRelease string `json:"sinceRelease"`
	Summary      string `json:"summary"`
	// Command is the exact command path reported in the command-result
	// `command` field. Empty for planned entries.
	Command      string         `json:"command,omitempty"`
	Argv         []string       `json:"argv"`
	OptionalArgs []OptionalArgs `json:"optionalArgs"`
	Mutates      bool           `json:"mutates"`
	Requires     Requirements   `json:"requires"`
	Inputs       []Input        `json:"inputs"`
	Results      []Outcome      `json:"results"`
	Events       []EventPhase   `json:"events"`
	Modes        Modes          `json:"modes"`
}

type Requirements struct {
	Capability          bool   `json:"capability"`
	CapabilityOperation string `json:"capabilityOperation,omitempty"`
	TrustImported       bool   `json:"trustImported"`
	OwnerApproval       bool   `json:"ownerApproval"`
	CandidateSpec       bool   `json:"candidateSpec"`
	ChangeSet           bool   `json:"changeSet"`
}

// Input binds an argv placeholder to the document contract it names.
type Input struct {
	Placeholder string `json:"placeholder"`
	ContractRef
	Description string `json:"description"`
}

// Outcome is the data payload of one command-result status.
type Outcome struct {
	Status string `json:"status"`
	ContractRef
	Description string `json:"description,omitempty"`
}

// EventPhase is one rollout event phase (exact) or phase family (prefix).
type EventPhase struct {
	Phase    string   `json:"phase"`
	Match    string   `json:"match"`
	Statuses []string `json:"statuses"`
}

// Modes records admission per lifecycle mode: standard is the local
// Owner-governed mode without a capability, advanced is the
// capability-gated mode an orchestrator dispatches.
type Modes struct {
	Standard string `json:"standard"`
	Advanced string `json:"advanced"`
}

func ref(schemaVersion, schema string) ContractRef {
	return ContractRef{SchemaVersion: schemaVersion, Schema: schema}
}

var (
	capabilityInput = Input{
		Placeholder: "capabilityFile",
		ContractRef: ref(CapabilitySchemaVersion, CapabilitySchema),
		Description: "Canonical Techstack-signed capability whose allowedOperations contains the operation, verified offline against the imported trust.",
	}
	candidateInput = Input{
		Placeholder: "candidateSpecFile",
		ContractRef: ref("stackkit/v2alpha2", "docs/stack-spec-reference.md"),
		Description: "Candidate StackSpec (apiVersion stackkit/v2alpha2) with generation.target terramate; the reference document is the authority.",
	}
	changeSetInput = Input{
		Placeholder: "changeSetId",
		ContractRef: ref(ChangeSetSchemaVersion, ChangeSetRecordSchema),
		Description: "Content address of the Owner-signed change-set record created by terramate.change-set.create; {changeSetSha256} pins its exact stored bytes.",
	}
	denialOutcome = Outcome{
		Status:      "denied",
		ContractRef: ref(OperationDenialSchemaVersion, OperationDenialSchema),
		Description: "Fail-closed admission denial before any side effect.",
	}
	// changeSetPrepareEvents mark each heavy admission phase (resolve
	// baseline, resolve candidate, render baseline, render candidate, diff)
	// before it starts, so a stuck or killed run names its phase.
	changeSetPrepareEvents = EventPhase{Phase: "advanced.change-set.prepare.", Match: "prefix", Statuses: []string{"started"}}
	changeSetEvents        = []EventPhase{
		changeSetPrepareEvents,
		{Phase: "advanced.change-set.materialize-host", Match: "exact", Statuses: []string{"started", "succeeded", "failed"}},
		{Phase: "advanced.change-set.run-order", Match: "exact", Statuses: []string{"started", "succeeded", "failed"}},
		{Phase: "advanced.change-set.converge", Match: "exact", Statuses: []string{"converged", "drifted", "failed", "pending_root", "other_host"}},
	}
	driftStackEvents = EventPhase{
		Phase: advanceddrift.EventPhase, Match: "exact",
		Statuses: []string{"converged", "drifted", "failed", "pending_root"},
	}
	capabilityModes = Modes{Standard: AdmissionDenied, Advanced: AdmissionCapability}
)

// New returns the catalog. Entries are sorted by operation ID.
func New() Catalog {
	return Catalog{
		SchemaVersion: SchemaVersion,
		Program:       Program,
		Description: "Advanced operations an orchestrator may dispatch to the stackkit CLI of the exact pinned release. " +
			"Argv words follow the program name; every {placeholder} is replaced by exactly one argv word and never by shell text. " +
			"Only entries with status available may be dispatched. The catalog shipped in the pinned release archive is authoritative for that release.",
		CommandResult: CommandResult{
			ContractRef: ref(CommandResultSchemaVersion, CommandResultSchema),
			Statuses:    []string{"success", "failed", "denied"},
			NoEnvelope: "A nonzero exit without one stackkit.command-result/v1 document on stdout is an unclassified failure; " +
				"stderr carries the message. Treat it as failed and never retry a mutating operation blindly.",
		},
		RolloutEvents: RolloutEvents{
			ContractRef: ref(RolloutEventSchemaVersion, RolloutEventSchema),
			Argv:        []string{"--progress-jsonl", "{progressFile}"},
			Description: "Redacted JSONL progress written to a file; `-` (stdout) is refused together with --json. " +
				"Each operation lists its Advanced phases; generic lifecycle phases of the governed transaction may also appear.",
		},
		Denial: Denial{
			ContractRef:         ref(OperationDenialSchemaVersion, OperationDenialSchema),
			CommandResultStatus: "denied",
			ReasonCodes:         reasonCodes(),
		},
		GlobalArgs: []OptionalArgs{
			{Argv: []string{"--chdir", "{workspaceDir}"}, Description: "Run against the StackKit workspace directory."},
			{Argv: []string{"--correlation-id", "{correlationId}"}, Description: "Validated caller correlation ID recorded in local rollout evidence."},
		},
		Placeholders: placeholders(),
		Operations: []Operation{
			{
				Operation: OperationTrustImport, Status: StatusAvailable, SinceRelease: "v0.15.8",
				Summary:      "Import the exact pinned Techstack issuer trust bundle into local Owner custody; prerequisite of every capability-gated operation.",
				Command:      "stackkit advanced trust import",
				Argv:         []string{"advanced", "trust", "import", "--bundle", "{bundleFile}", "--expect-sha256", "{bundleSha256}", "--owner-approve", "--json"},
				OptionalArgs: []OptionalArgs{},
				Mutates:      true,
				Requires:     Requirements{OwnerApproval: true},
				Inputs: []Input{{
					Placeholder: "bundleFile", ContractRef: ref(TrustBundleSchemaVersion, TrustBundleSchema),
					Description: "Canonical trust bundle; {bundleSha256} pins its exact bytes.",
				}},
				Results: []Outcome{{Status: "success", ContractRef: ref(LocalTrustSchemaVersion, LocalTrustSchema)}},
				Events:  []EventPhase{},
				Modes:   Modes{Standard: AdmissionOwnerApproval, Advanced: AdmissionOwnerApproval},
			},
			{
				Operation: OperationTrustInspect, Status: StatusAvailable, SinceRelease: "v0.15.8",
				Summary:      "Read the verified local Advanced issuer trust.",
				Command:      "stackkit advanced trust inspect",
				Argv:         []string{"advanced", "trust", "inspect", "--json"},
				OptionalArgs: []OptionalArgs{},
				Requires:     Requirements{TrustImported: true},
				Inputs:       []Input{},
				Results:      []Outcome{{Status: "success", ContractRef: ref(LocalTrustSchemaVersion, LocalTrustSchema)}},
				Events:       []EventPhase{},
				Modes:        Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed},
			},
			{
				Operation: OperationDriftDetectAdvanced, Status: StatusAvailable, SinceRelease: "v0.15.8",
				Summary:      "Read-only drift report; under the terramate target it adds one stacks[] entry per local Terramate stack from a detailed-exitcode plan. Never applies.",
				Command:      "stackkit drift detect",
				Argv:         []string{"drift", "detect", "--json"},
				OptionalArgs: []OptionalArgs{},
				Requires:     Requirements{},
				Inputs:       []Input{},
				Results: []Outcome{
					{Status: "success", ContractRef: ref(DriftReportSchemaVersion, DriftReportSchema), Description: "mode is advanced and stacks[] is present under the terramate generation target."},
					{Status: "failed", ContractRef: ref(ActionableErrorSchemaVersion, ActionableErrorSchema)},
					{Status: "denied", ContractRef: ref(ActionableErrorSchemaVersion, ActionableErrorSchema)},
				},
				Events: []EventPhase{driftStackEvents},
				Modes:  Modes{Standard: AdmissionAllowed, Advanced: AdmissionAllowed},
			},
			{
				Operation: advancedcapability.OperationDriftReconcileAdvanced, Status: StatusAvailable, SinceRelease: "v0.15.8",
				Summary: "Reconcile drift by applying an exact Owner-signed change set through the governed transaction, then prove a clean post-reconcile drift report including every stack plan.",
				Command: "stackkit drift reconcile",
				Argv: []string{"drift", "reconcile", "--mode", "advanced", "--capability", "{capabilityFile}", "--candidate-spec", "{candidateSpecFile}",
					"--change-set", "{changeSetId}", "--expect-sha256", "{changeSetSha256}", "--json"},
				OptionalArgs: []OptionalArgs{},
				Mutates:      true,
				Requires: Requirements{
					Capability: true, CapabilityOperation: advancedcapability.OperationDriftReconcileAdvanced,
					TrustImported: true, CandidateSpec: true, ChangeSet: true,
				},
				Inputs: []Input{capabilityInput, candidateInput, changeSetInput},
				Results: []Outcome{
					{Status: "success", ContractRef: ref(AdvancedMutationSchemaVersion, AdvancedMutationSchema), Description: "Carries data.driftReport observed after the reconcile."},
					{Status: "failed", ContractRef: ref(AdvancedMutationSchemaVersion, AdvancedMutationSchema), Description: "The transaction rolled back or the post-reconcile report is not clean."},
					denialOutcome,
				},
				Events: append(append([]EventPhase{}, changeSetEvents...), driftStackEvents),
				Modes:  capabilityModes,
			},
			{
				Operation: advancedcapability.OperationRestoreDrill, Status: StatusAvailable, SinceRelease: SincePending,
				Summary: "Stage and verify a restore of a new or given backup anchor without activation; the live runtime is verified unchanged.",
				Command: "stackkit advanced restore-drill run",
				Argv:    []string{"advanced", "restore-drill", "run", "--capability", "{capabilityFile}", "--owner-approve", "--json"},
				OptionalArgs: []OptionalArgs{
					{Argv: []string{"--anchor", "{anchorId}"}, Description: "Drill an existing snapshot anchor in local custody instead of creating one."},
					{Argv: []string{"--operation-id", "{operationId}"}, Description: "Stable drill ID; generated when omitted."},
				},
				Mutates: true,
				Requires: Requirements{
					Capability: true, CapabilityOperation: advancedcapability.OperationRestoreDrill,
					TrustImported: true, OwnerApproval: true,
				},
				Inputs: []Input{capabilityInput},
				Results: []Outcome{
					{Status: "success", ContractRef: ref(RestoreDrillReportSchemaVersion, RestoreDrillReportSchema)},
					{Status: "failed", ContractRef: ref(RestoreDrillReportSchemaVersion, RestoreDrillReportSchema)},
					denialOutcome,
				},
				Events: []EventPhase{{
					Phase: "advanced.restore-drill.", Match: "prefix",
					Statuses: []string{"started", "succeeded", "failed", "skipped"},
				}},
				Modes: capabilityModes,
			},
			rollbackEntry(),
			{
				Operation: advancedcapability.OperationTerramateChangeSetApply, Status: StatusAvailable, SinceRelease: "v0.15.8",
				Summary: "Apply an exact Owner-signed change set through the governed transaction and prove every affected Terramate stack converged; any failure rolls back.",
				Command: "stackkit advanced change-set apply",
				Argv: []string{"advanced", "change-set", "apply", "--capability", "{capabilityFile}", "--candidate-spec", "{candidateSpecFile}",
					"--change-set", "{changeSetId}", "--expect-sha256", "{changeSetSha256}", "--json"},
				OptionalArgs: []OptionalArgs{},
				Mutates:      true,
				Requires: Requirements{
					Capability: true, CapabilityOperation: advancedcapability.OperationTerramateChangeSetApply,
					TrustImported: true, CandidateSpec: true, ChangeSet: true,
				},
				Inputs: []Input{capabilityInput, candidateInput, changeSetInput},
				Results: []Outcome{
					{Status: "success", ContractRef: ref(AdvancedMutationSchemaVersion, AdvancedMutationSchema), Description: "data.changeSetResult is " + ChangeSetResultSchemaVersion + " (" + ChangeSetResultSchema + ")."},
					{Status: "failed", ContractRef: ref(AdvancedMutationSchemaVersion, AdvancedMutationSchema), Description: "transaction.status is rolled-back when the rollback restored the prior state."},
					{Status: "denied", ContractRef: ref(AdvancedMutationSchemaVersion, AdvancedMutationSchema), Description: "Admission denied before any side effect; this command reports the mutation record, not " + OperationDenialSchemaVersion + "."},
				},
				Events: changeSetEvents,
				Modes:  capabilityModes,
			},
			{
				Operation: advancedcapability.OperationTerramateChangeSetCreate, Status: StatusAvailable, SinceRelease: "v0.15.8",
				Summary:      "Create an Owner-signed change set from the candidate StackSpec: changed artifacts, affected Terramate stacks in run order and the host manifest digest.",
				Command:      "stackkit advanced change-set create",
				Argv:         []string{"advanced", "change-set", "create", "--capability", "{capabilityFile}", "--candidate-spec", "{candidateSpecFile}", "--json"},
				OptionalArgs: []OptionalArgs{},
				Mutates:      true,
				Requires: Requirements{
					Capability: true, CapabilityOperation: advancedcapability.OperationTerramateChangeSetCreate,
					TrustImported: true, CandidateSpec: true,
				},
				Inputs: []Input{capabilityInput, candidateInput},
				Results: []Outcome{
					{Status: "success", ContractRef: ref(ChangeSetSchemaVersion, ChangeSetCreateResultSchema), Description: "Summary of the stored " + ChangeSetSchemaVersion + " record (" + ChangeSetRecordSchema + ")."},
					denialOutcome,
				},
				Events: []EventPhase{changeSetPrepareEvents},
				Modes:  capabilityModes,
			},
		},
	}
}

// Render returns the deterministic catalog bytes.
func Render() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(New()); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func placeholders() []Placeholder {
	sha := `^sha256:[0-9a-f]{64}$`
	return []Placeholder{
		{Name: "anchorId", Description: "Existing snapshot-anchor ID in local custody."},
		{Name: "bundleFile", Description: "Path to the canonical Advanced trust bundle file."},
		{Name: "bundleSha256", Description: "Exact digest of the trust bundle bytes.", Pattern: sha},
		{Name: "candidateSpecFile", Description: "Path to the candidate StackSpec v2 file."},
		{Name: "capabilityFile", Description: "Path to the canonical capability file."},
		{Name: "changeSetId", Description: "Content address of the Owner-signed change set.", Pattern: sha},
		{Name: "changeSetSha256", Description: "Exact digest of the stored change-set bytes.", Pattern: sha},
		{Name: "correlationId", Description: "Caller correlation ID."},
		{Name: "operationId", Description: "Stable lowercase operation ID.", Pattern: `^[a-z0-9][a-z0-9._-]{7,99}$`},
		{Name: "progressFile", Description: "Path the rollout event JSONL is written to; never `-` together with --json."},
		{Name: "targetRef", Description: "Rollback target: an executor-state snapshot ID, or the change-set ID whose pre-apply checkpoint is the target.", Pattern: sha},
		{Name: "workspaceDir", Description: "StackKit workspace directory."},
	}
}

func reasonCodes() []ReasonCode {
	codes := []ReasonCode{
		{string(advancedcapability.ReasonTrustBundleUnavailable), "No Owner-approved issuer trust is imported; run advanced.trust.import."},
		{string(advancedcapability.ReasonCapabilityRequired), "The operation requires a capability."},
		{string(advancedcapability.ReasonCapabilityUnavailable), "No capability file was supplied."},
		{string(advancedcapability.ReasonCapabilityMalformed), "The capability is not the canonical stackkit.advanced-capability/v1 form."},
		{string(advancedcapability.ReasonCapabilityUntrustedKey), "The capability key is not in the imported trust."},
		{string(advancedcapability.ReasonCapabilitySignatureInvalid), "The capability signature does not verify."},
		{string(advancedcapability.ReasonCapabilityNotYetValid), "issuedAt is in the future."},
		{string(advancedcapability.ReasonCapabilityExpired), "expiresAt has passed; issue a new capability."},
		{string(advancedcapability.ReasonCapabilityLifetimeExceeded), "The capability lifetime exceeds the maximum."},
		{string(advancedcapability.ReasonCapabilityScopeMismatch), "stackId, ownerRef or another scoped claim does not match the local stack."},
		{string(advancedcapability.ReasonCapabilityOperationDenied), "allowedOperations does not contain the operation."},
		{string(advancedcapability.ReasonAdvancedChangeSetInvalid), "The change-set request or record is invalid."},
		{string(advancedcapability.ReasonAdvancedChangeSetStale), "The change set no longer matches the current baseline; create a new one."},
		{"advanced_change_set_io", "The change-set record could not be read or written."},
		{"owner_approval_required", "The operation requires --owner-approve."},
		{"restore_drill_request_invalid", "The restore-drill request is invalid (anchor, operation ID or workspace state)."},
		{"advanced_rollback_request_invalid", "The rollback request is invalid (--to is not a sha256 snapshot or change-set ID)."},
		{"terramate_tool_missing", "The packaged Terramate or OpenTofu binary is absent."},
		{"invalid_reconcile_mode", "drift reconcile --mode is neither standard nor advanced."},
	}
	return codes
}
