package usecasecatalog

// ModuleLoads projects the per-tier load declarations of the use cases
// (ADR-0037 vocabulary: residency, baseline, burst) onto the module IDs of the
// alternative that each tier names through moduleSlug. The result is keyed by
// module ID, then by compute profile ID: a tier and a module compute profile
// share the low, standard and high vocabulary, and a module only receives the
// load of the profile a plan actually selects.
//
// Only an included tier with a declared load and a moduleSlug that resolves to
// an alternative contributes. Two declarations that disagree for the same
// module and profile are dropped from the result, so the load stays unknown
// rather than taking whichever declaration happened to come first.
func ModuleLoads(useCases []UseCase) map[string]map[string]UseCaseLoad {
	loads := map[string]map[string]UseCaseLoad{}
	contested := map[[2]string]bool{}
	for _, useCase := range useCases {
		for profile, fit := range useCase.ComputeTiers {
			if !fit.Included || fit.Load == nil || fit.ModuleSlug == "" {
				continue
			}
			for _, alternative := range useCase.Alternatives {
				if alternative.ID != fit.ModuleSlug {
					continue
				}
				for _, module := range alternative.Modules {
					key := [2]string{module.ID, profile}
					if contested[key] {
						continue
					}
					byProfile := loads[module.ID]
					if byProfile == nil {
						byProfile = map[string]UseCaseLoad{}
						loads[module.ID] = byProfile
					}
					if existing, declared := byProfile[profile]; declared && existing != *fit.Load {
						delete(byProfile, profile)
						contested[key] = true
						continue
					}
					byProfile[profile] = *fit.Load
				}
			}
		}
	}
	for module, byProfile := range loads {
		if len(byProfile) == 0 {
			delete(loads, module)
		}
	}
	return loads
}
