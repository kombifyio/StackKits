// Package base - StackKit operations contract.
package base

#OperationPhase: "post_apply" | "reconcile" | "upgrade" | "pre_destroy"

#OperationExecutor: "go" | "pulumi"

#ResourceOwner: "opentofu" | "stackkit-runtime" | "pulumi" | "external"

#ApprovalMode: "implicit" | "required" | "denied"

#OperationValue: {
	ref?:   string
	value?: _
}

#ApprovalPolicy: {
	mode:    #ApprovalMode
	reason?: string
}

#StateScope: {
	backend: string
	stack:   string
}

// #OperationSpec is the StackKit-owned contract for post-apply and day-2
// operations. TechStack may orchestrate these operations, but StackKits owns
// the declaration and the resource ownership boundary.
#OperationSpec: {
	name:      =~"^[a-z][a-z0-9-]+$"
	phase:     #OperationPhase
	executor:  #OperationExecutor
	stateful:  bool
	owner:     #ResourceOwner
	provider?: string
	inputs?: [string]:  #OperationValue
	outputs?: [string]: #OperationValue
	secret_refs?: [...=~"^secret://"]
	approval_policy: #ApprovalPolicy
	if stateful {
		state_scope: #StateScope
	}
	if !stateful {
		state_scope?: _|_
	}
	if executor == "pulumi" {
		stateful: true
		owner:    "pulumi"
		provider: !~"(?i)^command$"
	}
}

#OwnedResource: {
	id:    string
	kind:  string
	owner: #ResourceOwner
}

#OwnershipManifest: {
	resources: [...#OwnedResource]
}
