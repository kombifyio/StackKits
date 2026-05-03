package base

_test_owner_local: #PocketIDOwner & {
	source:   "local"
	email:    "owner@example.com"
	username: "owner"
}

_test_owner_cloud: #PocketIDOwner & {
	source:           "cloud"
	email:            "owner@example.com"
	username:         "owner"
	foreignSubjectId: "auth0|abc123"
}

_test_breakglass_default:      #PocketIDBreakGlass & {}
_test_tinyauth_static_default: #TinyAuthStaticCred & {}

_test_bundle: #BreakGlassBundle & {
	nodeName: "homelab-pi-01"
}
