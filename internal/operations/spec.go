package operations

import (
	"errors"
	"fmt"
	"strings"
)

type Phase string

const (
	PhasePostApply  Phase = "post_apply"
	PhaseReconcile  Phase = "reconcile"
	PhaseUpgrade    Phase = "upgrade"
	PhasePreDestroy Phase = "pre_destroy"
)

type Executor string

const (
	ExecutorGo     Executor = "go"
	ExecutorPulumi Executor = "pulumi"
)

type Owner string

const (
	OwnerOpenTofu        Owner = "opentofu"
	OwnerStackKitRuntime Owner = "stackkit-runtime"
	OwnerPulumi          Owner = "pulumi"
	OwnerExternal        Owner = "external"
)

type ApprovalMode string

const (
	ApprovalImplicit ApprovalMode = "implicit"
	ApprovalRequired ApprovalMode = "required"
	ApprovalDenied   ApprovalMode = "denied"
)

type OperationValue struct {
	Ref   string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Value any    `json:"value,omitempty" yaml:"value,omitempty"`
}

type ApprovalPolicy struct {
	Mode   ApprovalMode `json:"mode" yaml:"mode"`
	Reason string       `json:"reason,omitempty" yaml:"reason,omitempty"`
}

type StateScope struct {
	Backend string `json:"backend" yaml:"backend"`
	Stack   string `json:"stack" yaml:"stack"`
}

type OperationSpec struct {
	Name           string                    `json:"name" yaml:"name"`
	Phase          Phase                     `json:"phase" yaml:"phase"`
	Executor       Executor                  `json:"executor" yaml:"executor"`
	Stateful       bool                      `json:"stateful" yaml:"stateful"`
	Owner          Owner                     `json:"owner" yaml:"owner"`
	Provider       string                    `json:"provider,omitempty" yaml:"provider,omitempty"`
	Inputs         map[string]OperationValue `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Outputs        map[string]OperationValue `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	SecretRefs     []string                  `json:"secret_refs,omitempty" yaml:"secret_refs,omitempty"`
	ApprovalPolicy ApprovalPolicy            `json:"approval_policy" yaml:"approval_policy"`
	StateScope     *StateScope               `json:"state_scope,omitempty" yaml:"state_scope,omitempty"`
}

func (s OperationSpec) Validate() error {
	var errs []string
	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, "name is required")
	}
	if !validPhase(s.Phase) {
		errs = append(errs, fmt.Sprintf("phase %q is not supported", s.Phase))
	}
	if !validExecutor(s.Executor) {
		errs = append(errs, fmt.Sprintf("executor %q is not supported", s.Executor))
	}
	if !validOwner(s.Owner) {
		errs = append(errs, fmt.Sprintf("owner %q is not supported", s.Owner))
	}
	if !validApprovalMode(s.ApprovalPolicy.Mode) {
		errs = append(errs, fmt.Sprintf("approval_policy.mode %q is not supported", s.ApprovalPolicy.Mode))
	}
	for key, value := range s.Inputs {
		if strings.TrimSpace(key) == "" {
			errs = append(errs, "inputs contain an empty key")
		}
		if err := validateOperationValue("inputs."+key, value); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for key, value := range s.Outputs {
		if strings.TrimSpace(key) == "" {
			errs = append(errs, "outputs contain an empty key")
		}
		if err := validateOperationValue("outputs."+key, value); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, ref := range s.SecretRefs {
		if !strings.HasPrefix(ref, "secret://") {
			errs = append(errs, fmt.Sprintf("secret_refs entry %q must start with secret://", ref))
		}
	}
	if s.Executor == ExecutorPulumi {
		if strings.EqualFold(strings.TrimSpace(s.Provider), "command") {
			errs = append(errs, "pulumi command provider is not allowed in StackKit operation specs")
		}
		if !s.Stateful {
			errs = append(errs, "pulumi operations must be stateful")
		}
		if s.Owner != OwnerPulumi {
			errs = append(errs, "pulumi operations must declare owner pulumi")
		}
	}
	if s.Stateful {
		if s.StateScope == nil {
			errs = append(errs, "state_scope is required for stateful operations")
		} else {
			if strings.TrimSpace(s.StateScope.Backend) == "" {
				errs = append(errs, "state_scope.backend is required")
			}
			if strings.TrimSpace(s.StateScope.Stack) == "" {
				errs = append(errs, "state_scope.stack is required")
			}
		}
	} else if s.StateScope != nil {
		errs = append(errs, "state_scope is only valid for stateful operations")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func validateOperationValue(path string, value OperationValue) error {
	hasRef := strings.TrimSpace(value.Ref) != ""
	hasValue := value.Value != nil
	if hasRef == hasValue {
		return fmt.Errorf("%s must set exactly one of ref or value", path)
	}
	return nil
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhasePostApply, PhaseReconcile, PhaseUpgrade, PhasePreDestroy:
		return true
	default:
		return false
	}
}

func validExecutor(executor Executor) bool {
	switch executor {
	case ExecutorGo, ExecutorPulumi:
		return true
	default:
		return false
	}
}

func validOwner(owner Owner) bool {
	switch owner {
	case OwnerOpenTofu, OwnerStackKitRuntime, OwnerPulumi, OwnerExternal:
		return true
	default:
		return false
	}
}

func validApprovalMode(mode ApprovalMode) bool {
	switch mode {
	case ApprovalImplicit, ApprovalRequired, ApprovalDenied:
		return true
	default:
		return false
	}
}
