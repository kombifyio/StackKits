package cue

const (
	ProductionGradeProduction      = "production"
	ProductionGradeLocalProduction = "local-production"
	ProductionGradeReference       = "reference"
	ProductionGradeExperimental    = "experimental"

	ProductionAuthPolicyLoginGateway = "login-gateway"
	ProductionAuthPolicySelfAuth     = "self-auth"
	ProductionAuthPolicyPublic       = "public"
	ProductionAuthPolicyInternal     = "internal"

	ProductionBackupRestoreNone = "not-applicable"
)

// ProductionServicePolicy is the Go representation of base.#ProductionServicePolicy.
type ProductionServicePolicy struct {
	Grade     string
	Auth      ProductionAuthPolicy
	FirstRun  ProductionFirstRunPolicy
	Health    ProductionHealthPolicy
	Backup    ProductionBackupPolicy
	Resources ProductionResourceBudget
}

type ProductionAuthPolicy struct {
	Policy          string
	Middleware      string
	ExceptionReason string
}

type ProductionFirstRunPolicy struct {
	Behavior string
	State    string
	Note     string
}

type ProductionHealthPolicy struct {
	Smoke string
	Path  string
	Port  int
}

type ProductionBackupPolicy struct {
	Required bool
	Restore  string
	Volumes  []string
}

type ProductionResourceBudget struct {
	Profile string
	Memory  string
	CPUs    float64
}
