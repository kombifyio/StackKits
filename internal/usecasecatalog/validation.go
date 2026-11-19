package usecasecatalog

import (
	"fmt"
	"strings"
)

// platformServiceImages names the image repositories of services the kit
// platform owns (docs/USE_CASE_STANDARD.md C8): reverse proxy, identity
// provider, certificate authority, backup engine and secret store. A use case
// consumes them and never ships its own.
var platformServiceImages = map[string]string{
	"library/traefik":          "reverse proxy",
	"jc21/nginx-proxy-manager": "reverse proxy",
	"keycloak/keycloak":        "identity provider",
	"bitnami/keycloak":         "identity provider",
	"goauthentik/server":       "identity provider",
	"authelia/authelia":        "identity provider",
	"pocket-id/pocket-id":      "identity provider",
	"tinyauthapp/tinyauth":     "identity provider",
	"steveiliop56/tinyauth":    "identity provider",
	"smallstep/step-ca":        "certificate authority",
	"kopia/kopia":              "backup engine",
	"restic/restic":            "backup engine",
	"library/vault":            "secret store",
	"hashicorp/vault":          "secret store",
	"infisical/infisical":      "secret store",
}

// platformServiceKind reports which platform service an image ref provides,
// matching its normalised repository (registry, tag and digest removed; an
// official image gains the library/ namespace).
func platformServiceKind(ref string) string {
	repository := strings.SplitN(ref, "@", 2)[0]
	if slash, colon := strings.LastIndex(repository, "/"), strings.LastIndex(repository, ":"); colon > slash {
		repository = repository[:colon]
	}
	parts := strings.Split(repository, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		parts = parts[1:]
	}
	if len(parts) == 1 {
		parts = append([]string{"library"}, parts...)
	}
	return platformServiceImages[strings.Join(parts, "/")]
}

// validatePlatformOwnership refuses a use-case alternative whose module runs
// a platform-owned service (C8).
func validatePlatformOwnership(source sourceCatalog) error {
	modules := map[string]map[string]any{}
	for _, module := range source.catalogModules {
		modules[metadataID(module)] = module
	}
	for useCaseRef, workload := range source.Workloads {
		alternatives, _ := workload["alternatives"].([]any)
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			runtime, _ := modules[stringField(alternative, "moduleRef")]["runtime"].(map[string]any)
			components, _ := runtime["components"].([]any)
			for _, rawComponent := range components {
				component, _ := rawComponent.(map[string]any)
				image, _ := component["image"].(map[string]any)
				if kind := platformServiceKind(stringField(image, "ref")); kind != "" {
					return fmt.Errorf("use case %s alternative %s ships its own %s (%s); use cases consume the kit platform", useCaseRef, stringField(alternative, "id"), kind, stringField(image, "ref"))
				}
			}
		}
	}
	return nil
}

// validateBackupVolumesAllocated requires every backup-enabled component
// volume of an alternative that claims backup/restore to be a backed-up
// storage allocation of that alternative (docs/USE_CASE_STANDARD.md F2),
// because the local backup and restore read only allocations: a volume
// missing there would be skipped without notice. An alternative that owns
// backup-enabled storage without the capability is refused by
// validateBackupHasARestoreClaim.
func validateBackupVolumesAllocated(source sourceCatalog) error {
	modules := map[string]map[string]any{}
	for _, module := range source.catalogModules {
		modules[metadataID(module)] = module
	}
	for useCaseRef, workload := range source.Workloads {
		alternatives, _ := workload["alternatives"].([]any)
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			if !claimsBackupRestore(alternative) {
				continue
			}
			allocated := map[string]bool{}
			infrastructure, _ := alternative["infrastructure"].(map[string]any)
			storage, _ := infrastructure["storageAllocation"].(map[string]any)
			allocations, _ := storage["allocations"].([]any)
			for _, rawAllocation := range allocations {
				allocation, _ := rawAllocation.(map[string]any)
				allocated[stringField(allocation, "componentRef")+"/"+stringField(allocation, "volumeRef")] = boolField(allocation, "backup")
			}
			runtime, _ := modules[stringField(alternative, "moduleRef")]["runtime"].(map[string]any)
			components, _ := runtime["components"].([]any)
			for _, rawComponent := range components {
				component, _ := rawComponent.(map[string]any)
				volumes, _ := component["volumes"].([]any)
				for _, rawVolume := range volumes {
					volume, _ := rawVolume.(map[string]any)
					if !boolField(volume, "backup") {
						continue
					}
					ref := stringField(component, "id") + "/" + stringField(volume, "id")
					// A volume shared from another component is allocated
					// under its owner.
					if owner, shared := volume["sharedFrom"].(map[string]any); shared {
						ref = stringField(owner, "componentRef") + "/" + stringField(owner, "volumeRef")
					}
					if !allocated[ref] {
						return fmt.Errorf("use case %s alternative %s backs up volume %s without a backed-up storage allocation", useCaseRef, stringField(alternative, "id"), ref)
					}
				}
			}
		}
	}
	return nil
}

// validateBackupHasARestoreClaim refuses a standalone Compose alternative that
// owns backup-enabled storage but claims no backup/restore. Backup would
// capture its data while every restore of a plan that selects it stopped
// closed (docs/USE_CASE_STANDARD.md F2). A stateless alternative owns no such
// storage and keeps the claim false; restore skips it as holding nothing.
func validateBackupHasARestoreClaim(source sourceCatalog) error {
	modules := map[string]map[string]any{}
	for _, module := range source.catalogModules {
		modules[metadataID(module)] = module
	}
	for _, workload := range source.catalogWorkloads {
		if stringField(workload, "kind") != "application" {
			continue
		}
		alternatives, _ := workload["alternatives"].([]any)
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			if !hasDeliveryRow(alternative, "standalone-compose") || claimsStandaloneBackupRestore(alternative) {
				continue
			}
			module, found := modules[stringField(alternative, "moduleRef")]
			if !found {
				return fmt.Errorf("workload %s alternative %s names module %q that the catalog does not define; restore treats unresolvable storage as holding data", metadataID(workload), stringField(alternative, "id"), stringField(alternative, "moduleRef"))
			}
			if !ownsBackupEnabledStorage(alternative, module) {
				continue
			}
			return fmt.Errorf("workload %s alternative %s owns backup-enabled storage but its standalone-compose row claims no backup/restore; a plan that selects it could not be restored", metadataID(workload), stringField(alternative, "id"))
		}
	}
	return nil
}

// validateRestoreClaimHasBackupStorage refuses a standalone Compose alternative
// whose row claims backup/restore without a backup-enabled storage allocation.
// Restore admits a claiming workload only through its backed-up allocations,
// so such a claim would stop every restore of a plan that selects it. A
// stateless alternative keeps the claim false and restore skips it.
func validateRestoreClaimHasBackupStorage(source sourceCatalog) error {
	for _, workload := range source.catalogWorkloads {
		if stringField(workload, "kind") != "application" {
			continue
		}
		alternatives, _ := workload["alternatives"].([]any)
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			if !claimsStandaloneBackupRestore(alternative) || allocatesBackupStorage(alternative) {
				continue
			}
			return fmt.Errorf("workload %s alternative %s claims standalone-compose backup/restore but allocates no backup-enabled storage; a plan that selects it could not be restored", metadataID(workload), stringField(alternative, "id"))
		}
	}
	return nil
}

// allocatesBackupStorage reports whether an alternative has a backup-enabled
// storage allocation.
func allocatesBackupStorage(alternative map[string]any) bool {
	infrastructure, _ := alternative["infrastructure"].(map[string]any)
	storage, _ := infrastructure["storageAllocation"].(map[string]any)
	allocations, _ := storage["allocations"].([]any)
	for _, raw := range allocations {
		allocation, _ := raw.(map[string]any)
		if boolField(allocation, "backup") {
			return true
		}
	}
	return false
}

// validateBackedUpAllocationsAreClassified requires every backed-up allocation
// of an alternative to carry its semantic backupDataClass. The class-based
// source policy admits only classified volumes and the default content coverage
// only the classes it names, so an unclassified volume would make every backup
// of a plan that selects it stop at run time.
func validateBackedUpAllocationsAreClassified(source sourceCatalog) error {
	for _, workload := range source.catalogWorkloads {
		if stringField(workload, "kind") != "application" {
			continue
		}
		alternatives, _ := workload["alternatives"].([]any)
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			infrastructure, _ := alternative["infrastructure"].(map[string]any)
			backupSource, _ := infrastructure["backupSource"].(map[string]any)
			allocations, _ := backupSource["allocations"].([]any)
			for _, rawAllocation := range allocations {
				allocation, _ := rawAllocation.(map[string]any)
				if stringField(allocation, "backupDataClass") == "" {
					return fmt.Errorf("workload %s alternative %s backs up %s/%s without a backupDataClass; add it to _architectureV2VolumeBackupClasses", metadataID(workload), stringField(alternative, "id"), stringField(allocation, "componentRef"), stringField(allocation, "volumeRef"))
				}
			}
		}
	}
	return nil
}

// ownsBackupEnabledStorage reports whether an alternative backs up any data:
// a backup-enabled storage allocation or a backup-enabled module volume.
func ownsBackupEnabledStorage(alternative, module map[string]any) bool {
	if allocatesBackupStorage(alternative) {
		return true
	}
	runtime, _ := module["runtime"].(map[string]any)
	components, _ := runtime["components"].([]any)
	for _, rawComponent := range components {
		component, _ := rawComponent.(map[string]any)
		volumes, _ := component["volumes"].([]any)
		for _, rawVolume := range volumes {
			volume, _ := rawVolume.(map[string]any)
			if boolField(volume, "backup") {
				return true
			}
		}
	}
	return false
}

// validateRecoverySequences requires every recovery step of a package to name
// a backed-up storage allocation (component/volume) of each committed
// alternative (docs/USE_CASE_STANDARD.md E4), so a restore sequence never
// refers to data that no backup holds.
func validateRecoverySequences(source sourceCatalog) error {
	for useCaseRef, pkg := range source.Packages {
		recovery, _ := pkg["recovery"].(map[string]any)
		sequence, _ := recovery["sequence"].([]any)
		if len(sequence) == 0 {
			continue
		}
		committed := map[string]struct{}{}
		cells, _ := pkg["committed"].([]any)
		for _, raw := range cells {
			cell, _ := raw.(map[string]any)
			committed[stringField(cell, "alternative")] = struct{}{}
		}
		if len(committed) == 0 {
			return fmt.Errorf("use case %s declares a recovery sequence without a committed alternative", useCaseRef)
		}
		alternatives, _ := source.Workloads[useCaseRef]["alternatives"].([]any)
		for alternativeRef := range committed {
			backedUp := map[string]struct{}{}
			found := false
			for _, raw := range alternatives {
				alternative, _ := raw.(map[string]any)
				if stringField(alternative, "id") != alternativeRef {
					continue
				}
				found = true
				infrastructure, _ := alternative["infrastructure"].(map[string]any)
				storage, _ := infrastructure["storageAllocation"].(map[string]any)
				allocations, _ := storage["allocations"].([]any)
				for _, rawAllocation := range allocations {
					allocation, _ := rawAllocation.(map[string]any)
					if boolField(allocation, "backup") {
						backedUp[stringField(allocation, "componentRef")+"/"+stringField(allocation, "volumeRef")] = struct{}{}
					}
				}
			}
			if !found {
				return fmt.Errorf("use case %s commits unknown alternative %s", useCaseRef, alternativeRef)
			}
			for _, rawStep := range sequence {
				step, _ := rawStep.(string)
				if _, ok := backedUp[step]; !ok {
					return fmt.Errorf("use case %s recovery step %q is not a backed-up volume of %s", useCaseRef, step, alternativeRef)
				}
			}
		}
	}
	return nil
}
