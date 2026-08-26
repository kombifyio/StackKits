// Package mail defines the Private Mail use case package.
//
// Mail is explicitly post-1.0. SK-M1 records catalog intent only; module,
// placement, protocol, DNS, runtime, and lifecycle decisions remain deferred
// to the normal post-1.0 integration path.
package mail

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "mail"
		useCaseRef:  "mail"
		displayName: "Private Mail"
		version:     "0.1.0"
		layer:       "application"
		category:    "mail"
		lifecycle:   "draft"
		description: "Post-1.0 private mail delivery and mailbox intent centered on Stalwart, with implementation and operational authority deliberately deferred."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "stalwart"
			role:       "primary"
			required:   true
			rationale:  "Stalwart is the cataloged future default, but Mail is post-1.0 and no module or executable workload exists."
			capabilities: ["mail-delivery", "mailbox"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "post-1-0-mail"
	runtimeProfiles: "post-1-0-mail": {
		displayName: "Post-1.0 Mail Runtime"
		description: "A future use-case integration must select the module, protocols, DNS and reputation controls, storage, identity, backup, and runtime placement."
		realization: "oss"
		placementModes: []
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: ["Catalog-only placeholder: empty placement prevents this package from claiming a selectable 1.0 runtime."]
	}

	computeTiers: {
		low: {
			included: false
			reason: "Mail is post-1.0. A mail stack is always-on active-resident (SMTP/IMAP) and does not fit low until a lite contract exists."
		}
		standard: {
			included: false
			reason: "Post-1.0. Intended load is 24/7 inbound/outbound with interactive bursts when reading mail."
		}
		high: {
			included: false
			reason: "Post-1.0. Same functions as standard; reputation/filter extras would be high-graph substitutions."
		}
	}

	tools: stalwart: {
		moduleSlug: "stalwart"
		role:       "primary"
		required:   true
		rationale:  "Cataloged future mail implementation; module contract and protocol surface remain post-1.0 work."
		capabilities: ["mail-delivery", "mailbox"]
	}
}
