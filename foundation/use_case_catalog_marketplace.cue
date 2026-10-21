// Package foundation extends the central product-intent catalog.
// ADR-0049 and workspace D-2026-10-20-09 admit Runtipi as an alternative
// Marketplace and execution-platform target. Unimplemented alternatives are
// projected as recorded choices; this declaration admits no runtime adapter.
package foundation

#MainUseCaseTitles: marketplace: "Marketplace"

UseCaseCatalog: entries: marketplace: {
	slug:        "marketplace"
	displayName: "Marketplace"
	description: "Your own app store for applications you run on your homelab, at home and in the cloud."
	owner:       "stackkits"
	mainUseCase: id: "marketplace"
	docs: "/guides/stackkits/application-delivery"
	components: coolify: {
		id:   "coolify"
		name: "Coolify"
		role: "primary"
		kind: "application"
	}
	components: runtipi: {
		id:   "runtipi"
		name: "Runtipi"
		role: "alternative"
		kind: "application"
	}
}
