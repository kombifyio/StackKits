// Public Automation entry: only the explicit ntfy live relay. The internal
// automation engine is not part of the public export.

package foundation

UseCaseCatalog: entries: automation: {
	slug:        "automation"
	displayName: "Automation"
	description: "Explicit live notifications for connected clients on your own node through a private ntfy relay. Runs without a kombify plan."
	owner:       "stackkits"
	mainUseCase: id: "automation"
	docs: "/guides/stackkits/use-cases/automation"
	components: "ntfy-live": {id: "ntfy-live", name: "ntfy live relay", role: "primary", kind: "application"}
}
