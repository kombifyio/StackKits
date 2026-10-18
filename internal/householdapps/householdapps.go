// Package householdapps names the node apps an owner can grant a household
// member individually and the Pocket ID groups that carry the grants.
package householdapps

import "slices"

const (
	// OpenGroup admits a member to every user-tier app. It is the default and
	// the state of members created before per-app grants.
	OpenGroup = "household-open"
	// AppGroupPrefix names the on-demand group of one app's grants.
	AppGroupPrefix = "household-app-"
)

// Grantable lists the app IDs a member can be granted, including independently
// selected add-ons. The IDs equal the application workload refs behind the Pocket
// ID client IDs and the TinyAuth route service refs. Vault and the kombify
// account link are never member-grantable.
var Grantable = []string{
	"ai", "automation", "dev", "documents", "files", "game", "mail",
	"marketplace", "media", "media-audiobooks", "network", "photos", "remote", "smart-home",
}

// IsGrantable reports whether app is a member-grantable app ID.
func IsGrantable(app string) bool { return slices.Contains(Grantable, app) }

// AppGroup is the Pocket ID group holding one app's grants.
func AppGroup(app string) string { return AppGroupPrefix + app }
