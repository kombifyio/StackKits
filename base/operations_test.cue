package base

_validPulumiOperation: #OperationSpec & {
	name:     "cloudflare-access-policy"
	phase:    "post_apply"
	executor: "pulumi"
	stateful: true
	owner:    "pulumi"
	provider: "cloudflare"
	inputs: zone_id: ref: "tofu.outputs.cloudflare_zone_id"
	secret_refs: ["secret://cloudflare/api-token"]
	approval_policy: {
		mode:   "required"
		reason: "external access policy changes require preview approval"
	}
	state_scope: {
		backend: "s3"
		stack:   "techstack/tenant-1/stack-1/ops"
	}
}

_validGoOperation: #OperationSpec & {
	name:     "pocketid-owner-bootstrap"
	phase:    "post_apply"
	executor: "go"
	stateful: false
	owner:    "stackkit-runtime"
	inputs: admin_email: value: "owner@example.com"
	approval_policy: mode: "implicit"
}

_validOwnershipManifest: #OwnershipManifest & {
	resources: [
		{
			id:    "docker_container.traefik"
			kind:  "container"
			owner: "opentofu"
		},
		{
			id:    "pocketid:user/owner"
			kind:  "identity-user"
			owner: "stackkit-runtime"
		},
	]
}
