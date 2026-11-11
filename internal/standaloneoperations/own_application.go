package standaloneoperations

func init() {
	catalog = append(catalog, Contract{
		ID: "stackkit.app.own", ToolName: "stackkit_own_application", Title: "Operate an own application",
		Description: "Use to run one extra container image beside the homelab. Dry-run the exact action to review Compose, routing and backup, then repeat it with the returned digest; remove keeps data unless deletion is explicit.",
		Command:     []string{"app", "own", "--json", "--owner-approve"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true,
		Arguments: []Argument{
			enumFlagArg("action", "--action", "Exact lifecycle action", "render", "create", "update", "rollback", "stop", "remove", "status", "logs"),
			requiredFlagArg("name", ArgumentString, "--name", "Application DNS name without own-"),
			flagArg("document_file", ArgumentString, "--document", "Local Techstack techstack.own-application/v1 JSON file; custody references only"),
			flagArg("installation_file", ArgumentString, "--installation-file", "Offline context for dry_run only; never admitted for a live mutation"),
			flagArg("dry_run", ArgumentBoolean, "--dry-run", "Review inputs and render with no Docker, custody writes or machine access"),
			flagArg("resolved_image", ArgumentString, "--resolved-image", "Exact immutable image from dry review for a tagged execution"),
			flagArg("secrets_file", ArgumentString, "--secrets-file", "Private JSON input for this application namespace; never inline secret values"),
			flagArg("keep_secrets", ArgumentBoolean, "--keep-secrets", "Remove only: explicitly retain secrets with the retained volume"),
			flagArg("review_digest", ArgumentString, "--review-digest", "Exact review digest from dry-run of this action"),
			flagArg("operation_id", ArgumentString, "--operation-id", "Stable exact-call identity"),
			flagArg("delete_data", ArgumentBoolean, "--delete-data", "Explicitly delete data on remove; default retains it"),
		},
	})
}
