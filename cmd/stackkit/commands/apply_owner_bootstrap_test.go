package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
)

func TestRequireManagedIdentityBootstrapHandoffFailsWhenMissing(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)
	applyTenantDeployment = "dep-123"

	err := requireManagedIdentityBootstrapHandoff(t.TempDir(), &models.StackSpec{
		Owner: models.OwnerConfig{
			BootstrapMode: models.OwnerBootstrapModeAuto,
			Source:        models.OwnerSourceCloud,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "identity bootstrap handoff") {
		t.Fatalf("expected missing identity bootstrap handoff error, got %v", err)
	}
}

func TestResolveOwnerBootstrapForApplyUsesManagedEnvelope(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)
	applyTenantDeployment = "dep-123"
	tmp := t.TempDir()
	writeIdentityBootstrapEnvelope(t, tmp, models.OwnerAdminBootstrapEnvelope{
		Owner: models.OwnerConfig{
			BootstrapMode:            models.OwnerBootstrapModeAuto,
			Source:                   models.OwnerSourceCloud,
			Email:                    "owner@example.com",
			Username:                 "owner",
			DisplayName:              "Owner Example",
			CloudOIDCForeignSubject:  "auth0|owner",
			RecoveryPassphraseHash:   "",
			RecoveryMaterialRef:      "techstack://identity-bootstrap/deployments/dep-123/recovery",
			CloudOIDCClientSecretRef: "secret://ignored",
		},
		AdminEmail:              "owner@example.com",
		AdminUsername:           "owner",
		RecoveryPassphrasePlain: "plain recovery material",
	})

	got, shouldRun, err := resolveOwnerBootstrapForApply(tmp, &models.StackSpec{
		Owner: models.OwnerConfig{
			BootstrapMode: models.OwnerBootstrapModeAuto,
			Source:        models.OwnerSourceCloud,
		},
	})
	if err != nil {
		t.Fatalf("resolveOwnerBootstrapForApply returned error: %v", err)
	}
	if !shouldRun {
		t.Fatal("managed owner bootstrap should run when envelope exists")
	}
	if !got.Managed {
		t.Fatal("managed flag should be set")
	}
	if got.Owner.Source != models.OwnerSourceLocal {
		t.Fatalf("Owner.Source = %q, want local for VM-local provisioning", got.Owner.Source)
	}
	if got.Owner.Email != "owner@example.com" || got.Owner.Username != "owner" {
		t.Fatalf("unexpected owner identity: %+v", got.Owner)
	}
	if got.RecoveryPassphrasePlain != "plain recovery material" {
		t.Fatalf("RecoveryPassphrasePlain = %q", got.RecoveryPassphrasePlain)
	}
}

func TestResolveOwnerBootstrapForApplyAutoSkipsWithoutManagedDeployment(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	_, shouldRun, err := resolveOwnerBootstrapForApply(t.TempDir(), &models.StackSpec{
		Owner: models.OwnerConfig{
			BootstrapMode: models.OwnerBootstrapModeAuto,
			Source:        models.OwnerSourceCloud,
		},
	})
	if err != nil {
		t.Fatalf("resolveOwnerBootstrapForApply returned error: %v", err)
	}
	if shouldRun {
		t.Fatal("auto bootstrap without managed tenant deployment should remain a local no-op")
	}
}

func writeIdentityBootstrapEnvelope(t *testing.T, wd string, env models.OwnerAdminBootstrapEnvelope) {
	t.Helper()
	path := identityBootstrapEnvelopePath(wd)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir identity bootstrap dir: %v", err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal identity bootstrap envelope: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write identity bootstrap envelope: %v", err)
	}
}
