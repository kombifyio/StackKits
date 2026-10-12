// Package foundation extends the central product-intent catalog.
// ADR-0049 and the workspace decision of 2026-10-11 settle Coolify as the
// Marketplace application. This declaration admits no runtime alternative.
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
}
