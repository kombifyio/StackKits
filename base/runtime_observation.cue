// Package base - canonical provider-neutral runtime observation contract.
package base

#RuntimeObservationDigest: string & =~"^sha256:[0-9a-f]{64}$"
#RuntimeObservationRef:    string & =~"^[^[:space:]]+$"

// #RuntimeObservationV2 is the local lifecycle/read-model handoff consumed by
// the standalone CLI, MCP, and optional orchestrators. It deliberately has no
// provider, lease, credential, transport-lifecycle, or account fields.
#RuntimeObservationV2: close({
	schemaVersion: "stackkit.runtime-observation/v2"
	phase:         "apply" | "status" | "verify"
	source:        "local-runtime" | "standard-process" | "verified-apply-evidence"
	observedAt:    string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T.*Z$"
	live:          bool
	identity: close({
		stackId:             #NonEmptyString
		planHash:            #RuntimeObservationDigest
		applyResultHash:     #RuntimeObservationDigest
		runId:               #NonEmptyString
		siteRef:             #NonEmptyString
		nodeRef:             #NonEmptyString
		executionChannelRef: #NonEmptyString
	})
	services: [...close({
		ref:        #NonEmptyString
		url?:       #NonEmptyString
		status:     "running" | "stopped" | "starting" | "error" | "unknown"
		health:     "healthy" | "unhealthy" | "starting" | "not-required" | "unknown"
		healthRef?: #NonEmptyString
	})]
	runtime: [...close({
		requirementId:      #NonEmptyString
		instanceRef:        #NonEmptyString
		status:             #NonEmptyString
		observationRef:     #RuntimeObservationRef
		observationDigest:  #RuntimeObservationDigest
		siteRef:            #NonEmptyString
		nodeRef:            #NonEmptyString
		executionChannelRef: #NonEmptyString
	})]
	health: [...close({
		requirementId:     #NonEmptyString
		targetRef:         #NonEmptyString
		sourceRef?:        #NonEmptyString
		status:            #NonEmptyString
		observationRef:    #RuntimeObservationRef
		observationDigest: #RuntimeObservationDigest
		siteRef:           #NonEmptyString
		nodeRef:           #NonEmptyString
	})]
	evidenceLinks: [...close({
		kind:    #NonEmptyString
		ref:     #RuntimeObservationRef
		digest?: #RuntimeObservationDigest
	})]
})

// #ActionableErrorV1 is the stable recovery envelope returned by CLI and MCP
// adapters. Guidance is local and operator-executable; it never delegates
// Standard Mode recovery to a hosted product.
#ActionableErrorV1: close({
	schemaVersion: "stackkit.actionable-error/v1"
	code:          #NonEmptyString
	reasonCode:    #NonEmptyString
	message:       #NonEmptyString
	userGuidance: [#NonEmptyString, ...#NonEmptyString]
	retryable: bool
})
