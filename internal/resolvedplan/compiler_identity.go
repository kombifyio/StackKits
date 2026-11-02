package resolvedplan

// requireBoundCompiler reports a plan another compiler release produced as
// stale before any schema, Definition or catalog check runs. A newer release
// may change those contracts too, and the first failing check must not hide
// the one condition a caller can recover from: regenerate under this release.
// A plan without a readable compiler identity falls through to the full
// contract, which refuses it.
func (v *CUEContractValidator) requireBoundCompiler(plan ResolvedPlan) error {
	if v == nil || v.boundAuthority == nil || v.boundAuthority.compiler == "" {
		return nil
	}
	compiler, ok := plan["compilerVersion"].(string)
	if !ok || compiler == "" || compiler == v.boundAuthority.compiler {
		return nil
	}
	return &CompilerVersionMismatchError{PlanCompiler: compiler, AuthorityCompiler: v.boundAuthority.compiler}
}
