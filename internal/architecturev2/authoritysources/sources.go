package authoritysources

var contractFixture = []string{
	"cue.mod/module.cue",
	"foundation/use_case_identity.cue",
	"foundation/architecture_v2_profiles.cue",
	"foundation/architecture_v2.cue",
	"foundation/architecture_v2_storage.cue",
	"foundation/architecture_v2_backup.cue",
	"foundation/application_lifecycle.cue",
	"foundation/architecture_v2_definition_binding.cue",
	"foundation/architecture_v2_catalog.cue",
	"architecture/v2/contractfixture/catalog.cue",
	"basement-kit/stackfile.cue",
}

func ContractFixture() []string {
	return append([]string(nil), contractFixture...)
}
