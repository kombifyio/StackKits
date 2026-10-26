package platformdeploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"

	"gopkg.in/yaml.v3"
)

// CoolifyProviderRecipeDigest binds the complete recipe, including its labels,
// without retaining upstream contents in signed public deployment custody.
func CoolifyProviderRecipeDigest(recipe string) (string, error) {
	var graph map[string]any
	if recipe == "" || yaml.Unmarshal([]byte(recipe), &graph) != nil || len(graph) == 0 {
		return "", errors.New("Coolify provider recipe is invalid")
	}
	canonical, err := json.Marshal(graph)
	if err != nil {
		return "", errors.New("Coolify provider recipe cannot be canonicalized")
	}
	defer clear(canonical)
	hash := sha256.New()
	_, _ = hash.Write([]byte("stackkit.coolify-provider-custody/v1\x00"))
	_, _ = hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// RequireProviderService checks the held ID independently before provider
// refresh/import. Its name alone never authorizes adoption or replacement.
func (a *CoolifyAdapter) RequireProviderService(ctx context.Context, ref DeploymentRef, heldRecipeDigest string, expectedRecipes ...string) error {
	if ref.Platform != "coolify" || ref.ExternalID == "" || ref.AppName == "" ||
		a.cfg.ProjectUUID == "" || a.cfg.ServerID == "" || a.cfg.EnvironmentID != "production" ||
		(len(expectedRecipes) == 0 && heldRecipeDigest == "") ||
		(heldRecipeDigest != "" && !hostArtifactDigestPattern.MatchString(heldRecipeDigest)) {
		return errors.New("Coolify provider requires the held service and its node recipe")
	}
	var service struct {
		UUID, Name  string
		Compose     string `json:"docker_compose_raw"`
		Destination struct {
			Server struct{ UUID string }
		}
	}
	if _, _, err := a.client.getJSON(ctx, "/api/v1/services/"+url.PathEscape(ref.ExternalID), &service); err != nil {
		return errors.New("Coolify held service could not be read; inspect its exact ID before retrying")
	}
	if service.UUID != ref.ExternalID || service.Name != ref.AppName || service.Destination.Server.UUID != a.cfg.ServerID {
		return errors.New("Coolify service identity or node differs from signed custody")
	}
	var actual any
	matched := false
	if heldRecipeDigest != "" {
		digest, err := CoolifyProviderRecipeDigest(service.Compose)
		matched = err == nil && digest == heldRecipeDigest
	}
	if service.Compose != "" && yaml.Unmarshal([]byte(service.Compose), &actual) == nil {
		for _, recipe := range expectedRecipes {
			var expected any
			if recipe != "" && yaml.Unmarshal([]byte(recipe), &expected) == nil && reflect.DeepEqual(expected, actual) {
				matched = true
			}
		}
	}
	if !matched {
		return errors.New("Coolify remote recipe differs from node custody; reconcile explicitly before provider refresh")
	}
	var project struct {
		UUID         string
		Environments []struct{ UUID, Name string }
	}
	if _, _, err := a.client.getJSON(ctx, "/api/v1/projects/"+url.PathEscape(a.cfg.ProjectUUID), &project); err != nil || project.UUID != a.cfg.ProjectUUID || len(project.Environments) == 0 {
		return errors.New("Coolify held project graph could not be verified")
	}
	found := 0
	for _, environment := range project.Environments {
		if environment.UUID == "" {
			return errors.New("Coolify project contains an unidentified environment")
		}
		var graph struct {
			UUID     string
			Services []struct{ UUID string }
		}
		if _, _, err := a.client.getJSON(ctx, "/api/v1/projects/"+url.PathEscape(a.cfg.ProjectUUID)+"/"+url.PathEscape(environment.UUID), &graph); err != nil || graph.UUID != environment.UUID || graph.Services == nil {
			return errors.New("Coolify held environment graph could not be verified")
		}
		for _, candidate := range graph.Services {
			if candidate.UUID == ref.ExternalID {
				if environment.Name != "production" {
					return errors.New("Coolify held service left its production environment")
				}
				found++
			}
		}
	}
	if found != 1 {
		return errors.New("Coolify held service is not uniquely attached to the node's curated project")
	}
	return nil
}
