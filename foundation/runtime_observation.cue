// Package foundation - canonical provider-neutral runtime observation contract.
package foundation

#RuntimeObservationDigest: string & =~"^sha256:[0-9a-f]{64}$"
#RuntimeObservationRef:    string & =~"^[^[:space:]]+$"
#RuntimeObservationTimestamp: string & =~"^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\\.[0-9]{1,9})?Z$"
#RuntimeObservationReachableHTTPStatus:   (int & >=200 & <=399) | 401 | 403
#RuntimeObservationUnreachableHTTPStatus: (int & >=100 & <=199) | (int & >=400 & <=599 & !=401 & !=403)

#RuntimeObservationHTTPProbeV1: close({
	vantage:          "verifier-host"
	observedAt:       #RuntimeObservationTimestamp
	workloadRef?:     #RuntimeObservationRef
	serviceRef:       #RuntimeObservationRef
	routeRef:         #RuntimeObservationRef
	url:              string & =~"^(http|https)://[^@/?#[:space:]]+([/?][^#[:space:]]*)?$"
	method:           "GET"
	status:           "reachable" | "unreachable"
	reached:          bool
	statusCode?:      int & >=100 & <=599
	failureClass?:    "invalid-target" | "request-creation-failed" | "request-failed" | "response-read-failed" | "response-too-large" | "http-status"
	observationRef:   #RuntimeObservationRef
	observationDigest: #RuntimeObservationDigest
}) & ({
		status:     "reachable"
		reached:    true
		statusCode: #RuntimeObservationReachableHTTPStatus
		failureClass?: _|_
	} |
	{
		status:       "unreachable"
		reached:      false
		statusCode?:  #RuntimeObservationUnreachableHTTPStatus
		failureClass: "invalid-target" | "request-creation-failed" | "request-failed" | "response-read-failed" | "response-too-large" | "http-status"
	})

// #RuntimeObservationV2 is the local lifecycle/read-model handoff consumed by
// the standalone CLI, MCP, and optional orchestrators. It deliberately has no
// provider, lease, credential, transport-lifecycle, or account fields.
#RuntimeObservationV2: close({
	schemaVersion: "stackkit.runtime-observation/v2"
	phase:         "apply" | "status" | "verify"
	source:        "local-runtime" | "standard-process" | "verified-apply-evidence"
	observedAt:    #RuntimeObservationTimestamp
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
	httpProbes?: [...#RuntimeObservationHTTPProbeV1]
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
