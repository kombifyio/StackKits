package standaloneoperations

func init() {
	catalog = append(catalog, Contract{
		ID: "stackkit.app.own", ToolName: "stackkit_own_application", Title: "Operate an own application",
		Description: "Use to run one extra container image, or one Compose project of several containers, beside the homelab. Dry-run the exact action to review Compose, routing and backup, then repeat it with the returned digest; remove keeps data unless deletion is explicit.",
		Command:     []string{"app", "own", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			enumFlagArg("action", "--action", "Exact lifecycle action", "render", "create", "update", "rollback", "start", "stop", "restart", "remove", "status", "logs", "discard-checkpoint"),
			requiredFlagArg("name", ArgumentString, "--name", "Application DNS name without own-"),
			flagArg("document_file", ArgumentString, "--document", "Local Techstack own-application/v1, v2 or v3 JSON file; custody references only"),
			flagArg("installation_file", ArgumentString, "--installation-file", "Offline context for dry_run only; never admitted for a live mutation"),
			flagArg("dry_run", ArgumentBoolean, "--dry-run", "Review inputs and render with no Docker, custody writes or machine access"),
			flagArg("resolved_image", ArgumentString, "--resolved-image", "Exact immutable image from dry review for a tagged execution"),
			flagArg("image_tag", ArgumentString, "--image-tag", "Update only: newer version tag of the application's own image; re-resolved before stopping, checkpoint, verify, automatic rollback"),
			flagArg("allow_major", ArgumentBoolean, "--allow-major", "With image_tag: admit another major version or an unversioned running image; never a downgrade or another variant"),
			flagArg("checkpoint", ArgumentString, "--checkpoint", "Rollback: restore the data checkpoint this tag update operation took, then return to its prior image; discard-checkpoint: remove its retained copy"),
			flagArg("capability_file", ArgumentString, "--capability", "Managed node only: Techstack own-application.lifecycle capability bound to this application, action, operation ID and review digest"),
			flagArg("resolved_images_file", ArgumentString, "--resolved-images-file", "JSON object of service and digest pairs from dry review for a tagged project execution"),
			flagArg("secrets_file", ArgumentString, "--secrets-file", "Private JSON input for this application namespace; never inline secret values"),
			flagArg("keep_secrets", ArgumentBoolean, "--keep-secrets", "Remove only: explicitly retain secrets with the retained volume"),
			flagArg("review_digest", ArgumentString, "--review-digest", "Exact review digest from dry-run of this action"),
			flagArg("operation_id", ArgumentString, "--operation-id", "Stable exact-call identity"),
			flagArg("delete_data", ArgumentBoolean, "--delete-data", "Explicitly delete data on remove; default retains it"),
			flagArg("decision_receipt", ArgumentString, "--decision-receipt", "Compact decision receipt (JWS) that carries the Owner's confirmation of this exact removal; required when remove deletes data outside a dry run, refused otherwise"),
		},
	})
}
