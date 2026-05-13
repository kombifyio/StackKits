package operations

import (
	"strings"
	"testing"
)

func TestOperationSpecValidateAcceptsStatefulPulumiExternalAPI(t *testing.T) {
	spec := OperationSpec{
		Name:     "cloudflare-access-policy",
		Phase:    PhasePostApply,
		Executor: ExecutorPulumi,
		Stateful: true,
		Owner:    OwnerPulumi,
		Provider: "cloudflare",
		Inputs: map[string]OperationValue{
			"zone_id": {Ref: "tofu.outputs.cloudflare_zone_id"},
		},
		SecretRefs: []string{"secret://cloudflare/api-token"},
		ApprovalPolicy: ApprovalPolicy{
			Mode:   ApprovalRequired,
			Reason: "external access policy changes require preview approval",
		},
		StateScope: &StateScope{
			Backend: "s3",
			Stack:   "techstack/tenant-1/stack-1/ops",
		},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOperationSpecValidateRejectsPulumiWithoutStateScope(t *testing.T) {
	spec := OperationSpec{
		Name:           "auth0-client",
		Phase:          PhasePostApply,
		Executor:       ExecutorPulumi,
		Stateful:       true,
		Owner:          OwnerPulumi,
		Provider:       "auth0",
		ApprovalPolicy: ApprovalPolicy{Mode: ApprovalRequired},
	}

	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want state scope error")
	}
	if !strings.Contains(err.Error(), "state_scope is required") {
		t.Fatalf("Validate() error = %q, want state_scope error", err)
	}
}

func TestOperationSpecValidateRejectsPulumiCommandProvider(t *testing.T) {
	spec := OperationSpec{
		Name:           "shell-wrapper",
		Phase:          PhasePostApply,
		Executor:       ExecutorPulumi,
		Stateful:       true,
		Owner:          OwnerPulumi,
		Provider:       "command",
		ApprovalPolicy: ApprovalPolicy{Mode: ApprovalRequired},
		StateScope:     &StateScope{Backend: "s3", Stack: "techstack/tenant-1/stack-1/ops"},
	}

	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want command provider rejection")
	}
	if !strings.Contains(err.Error(), "command provider") {
		t.Fatalf("Validate() error = %q, want command provider rejection", err)
	}
}

func TestOwnershipManifestValidateRejectsDuplicateResourceOwners(t *testing.T) {
	manifest := OwnershipManifest{
		Resources: []OwnedResource{
			{ID: "pocketid:user/owner", Kind: "identity-user", Owner: OwnerStackKitRuntime},
			{ID: "pocketid:user/owner", Kind: "identity-user", Owner: OwnerPulumi},
		},
	}

	err := manifest.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want duplicate owner error")
	}
	if !strings.Contains(err.Error(), "multiple owners") {
		t.Fatalf("Validate() error = %q, want multiple owners error", err)
	}
}
