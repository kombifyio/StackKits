package base

_test_owner_local: #PocketIDOwner & {
	bootstrapMode:          "custom"
	source:                 "local"
	email:                  "owner@example.com"
	username:               "owner"
	recoveryPassphraseHash: "$argon2id$v=19$m=65536,t=3,p=4$dGVzdHNhbHQ$dGVzdGhhc2g"
}

_test_owner_cloud: #PocketIDOwner & {
	bootstrapMode:       "auto"
	source:              "cloud"
	recoveryMaterialRef: "techstack://recovery/scenarios/cloud-owner"
}

_test_owner_none: #PocketIDOwner & {
	bootstrapMode: "none"
}

_test_breakglass_default: #PocketIDBreakGlass & {}
_test_tinyauth_static_default: #TinyAuthStaticCred & {}
_test_recovery_handoff_ref: #RecoveryMaterialHandoff & {
	kind:  "ref"
	value: "techstack://recovery/scenarios/cloud-owner"
}

_test_bundle: #BreakGlassBundle & {
	nodeName: "homelab-pi-01"
}
