package cue

import (
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/testscenarios"
)

func TestCanonicalScenarioMissingMailContractFailsValidation(t *testing.T) {
	scenario, err := testscenarios.ByID("SK-S5")
	if err != nil {
		t.Fatalf("load SK-S5: %v", err)
	}

	result, err := NewValidator(".").ValidateSpec(&scenario.StackSpec)
	if err != nil {
		t.Fatalf("ValidateSpec returned error: %v", err)
	}
	if result.Valid {
		t.Fatalf("SK-S5 should fail validation without owner/admin email")
	}

	var messages []string
	for _, validationErr := range result.Errors {
		messages = append(messages, validationErr.Message)
	}
	if !strings.Contains(strings.Join(messages, "\n"), scenario.Expected.Failure.MessageContains) {
		t.Fatalf("validation errors %q do not contain %q", messages, scenario.Expected.Failure.MessageContains)
	}
}

func TestCanonicalScenarioPublicConfigsWithEmailValidate(t *testing.T) {
	for _, id := range []string{"SK-S2", "SK-S3", "SK-S4"} {
		scenario, err := testscenarios.ByID(id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		result, err := NewValidator(".").ValidateSpec(&scenario.StackSpec)
		if err != nil {
			t.Fatalf("ValidateSpec(%s) returned error: %v", id, err)
		}
		if !result.Valid {
			t.Fatalf("%s should validate: %#v", id, result.Errors)
		}
	}
}
