package authoritysources

var contractFixture = []string{
	"cue.mod/module.cue",
	"base/use_case_identity.cue",
	"base/architecture_v2_profiles.cue",
	"base/architecture_v2.cue",
	"base/architecture_v2_storage.cue",
	"base/architecture_v2_backup.cue",
	"base/application_lifecycle.cue",
	"base/architecture_v2_definition_binding.cue",
	"base/architecture_v2_catalog.cue",
	"architecture/v2/contractfixture/catalog.cue",
	"basement-kit/stackfile.cue",
}

func ContractFixture() []string {
	return append([]string(nil), contractFixture...)
}
