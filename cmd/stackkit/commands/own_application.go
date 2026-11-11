package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	agentdocs "github.com/kombifyio/stackkits/docs/agent"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"os"
	"path/filepath"
	"strings"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/ownapplication"
	"github.com/spf13/cobra"
)

func init() {
	appCmd.AddCommand(newOwnApplicationCommand())
	raw, _ := agentdocs.FS.ReadFile("homelab-agent.md")
	agentPromptBodies["homelab-agent"] = string(raw)
}
func newOwnApplicationCommand() *cobra.Command {
	var action, name, document, installationFile, operation, review, resolvedImage, secretsFile string
	var dry, approved, deleteData, keepSecrets, outputJSON bool
	c := &cobra.Command{Use: "own", Short: "Render or operate one Techstack-owned container on this node", Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		req := ownapplication.Request{Action: action, Name: name, OperationID: operation, ReviewDigest: review, DryRun: dry, OwnerApproved: approved, DeleteData: deleteData, KeepSecrets: keepSecrets, ResolvedImage: resolvedImage}
		if document != "" {
			raw, err := readOwnApplicationDocument(document)
			if err != nil {
				return err
			}
			a, err := contract.Parse(raw)
			if err != nil {
				return err
			}
			req.Application = &a
		}
		if secretsFile != "" {
			info, e := os.Lstat(secretsFile)
			if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return errors.New("secrets-file must be a private regular file (0600 or stricter)")
			}
			raw, e := readOwnApplicationDocument(secretsFile)
			if e != nil {
				return errors.New("private own-application secret input unavailable")
			}
			if contract.Decode(raw, &req.SecretMaterial) != nil {
				return errors.New("private secret input must be a JSON object of environment keys and values")
			}
			clear(raw)
		}
		var i contract.Installation
		if installationFile != "" {
			if !dry {
				return errors.New("--installation-file is inspection-only and requires --dry-run")
			}
			raw, err := readOwnApplicationDocument(installationFile)
			if err != nil {
				return err
			}
			if err = contract.Decode(raw, &i); err != nil {
				return err
			}
		} else {
			authority, err := inspectNativeV2AppliedAuthority(commandContext(cmd), getWorkDir(), specFile)
			if err != nil {
				return fmt.Errorf("own application requires an installed and verified Direct Compose node: %w", err)
			}
			if err = checkOwnApplicationCollisions(authority, name); err != nil {
				return err
			}
			i, err = ownApplicationInstallation(authority)
			if err == nil && (action == "create" || action == "update" || action == "rollback") {
				state, e := ownapplication.Load(getWorkDir())
				if e != nil {
					return e
				}
				hasVolume := req.Application != nil && req.Application.Volume != nil || state.Applications[name].Bundle.Application.Volume != nil
				if hasVolume {
					if e = requireOwnApplicationBackupCoverage(authority); e != nil {
						return e
					}
				}
			}
			if err != nil {
				return err
			}
		}
		result, err := ownapplication.Execute(commandContext(cmd), getWorkDir(), i, req)
		if err != nil {
			return err
		}
		// The public operation always returns its versioned result, including the
		// review digest and signed custody operation; --json is accepted explicitly.
		_ = outputJSON
		if ownapplication.Mutating(action) && !dry {
			runAutomaticBackupRebind(commandContext(cmd), getWorkDir())
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	c.Flags().StringVar(&action, "action", "render", "create, update, rollback, stop, remove, status, logs or render")
	c.Flags().StringVar(&name, "name", "", "Application DNS name, without the reserved own- prefix")
	c.Flags().StringVar(&document, "document", "", "Techstack techstack.own-application/v1 JSON document")
	c.Flags().StringVar(&installationFile, "installation-file", "", "Offline installation context; dry-run only")
	c.Flags().StringVar(&resolvedImage, "resolved-image", "", "Exact digest from dry review; required for executing a tagged image")
	c.Flags().StringVar(&secretsFile, "secrets-file", "", "Private JSON environment-key/value input for application-scoped custody")
	c.Flags().BoolVar(&keepSecrets, "keep-secrets", false, "On remove only: retain secrets with the retained named volume")
	c.Flags().StringVar(&operation, "operation-id", "", "Stable identity of this exact reviewed mutation")
	c.Flags().StringVar(&review, "review-digest", "", "Exact review digest returned by dry-run of this action")
	c.Flags().BoolVar(&dry, "dry-run", false, "Review inputs and render only; no Docker or custody writes")
	c.Flags().BoolVar(&approved, "owner-approve", false, "Approve this exact local operation")
	c.Flags().BoolVar(&deleteData, "delete-data", false, "Delete the named volume on remove; default retains it in backup")
	c.Flags().BoolVar(&outputJSON, "json", false, "Return the versioned result as JSON")
	return c
}
func readOwnApplicationDocument(name string) ([]byte, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer transaction.Close()
	raw, _, err := transaction.ReadStableBounded(filepath.Base(absolute), 64<<10)
	return raw, err
}
func ownApplicationInstallation(a nativeV2AppliedAuthority) (contract.Installation, error) {
	var p struct {
		Network struct {
			Configuration struct {
				Domain struct {
					Base string `json:"base"`
				} `json:"domain"`
			} `json:"configuration"`
			Routes []json.RawMessage `json:"routes"`
		} `json:"network"`
	}
	if err := json.Unmarshal(a.Plan.Canonical(), &p); err != nil {
		return contract.Installation{}, err
	}
	// The route and its issuer come from the signed applied bundle, with no
	// caller-selected TLS, exposure or daemon endpoint.
	for _, w := range a.AppliedWorkloads {
		deployment, err := nativeAppliedWorkloadDeploymentForBackup(a, w.WorkloadRef)
		if err != nil {
			continue
		}
		route := deployment.Route
		if route.Host == "" || !route.TLSRequired || route.TLSIssuerRef == "" {
			continue
		}
		if deployment.RuntimeAdapter.ID != "standalone-compose" || deployment.NodeRef != a.Owner.Binding.NodeRef || deployment.SiteRef != a.Owner.Binding.SiteRef {
			continue
		}
		domain := p.Network.Configuration.Domain.Base
		if domain == "" {
			continue
		}
		if !strings.HasSuffix(route.Host, "."+domain) {
			continue
		}
		return contract.Installation{Domain: domain, SiteRef: a.Owner.Binding.SiteRef, NodeRef: a.Owner.Binding.NodeRef, CoreModuleRef: route.CoreModuleRef, TLSIssuerRef: route.TLSIssuerRef, TLSProfileRef: route.TLSProfileRef, Exposure: route.Exposure}, nil
	}
	// A core-only installation still has exact renderer-owned route inputs.
	for _, raw := range p.Network.Routes {
		var route struct {
			Host, ModuleRef, CoreModuleRef, Exposure string
			TLS                                      struct {
				Required              bool
				IssuerRef, ProfileRef string
			}
		}
		if json.Unmarshal(raw, &route) != nil || !route.TLS.Required || route.TLS.IssuerRef == "" {
			continue
		}
		if route.CoreModuleRef == "" {
			route.CoreModuleRef = route.ModuleRef
		}
		if route.CoreModuleRef != "stackkits-basement-core-runtime" && route.CoreModuleRef != "stackkits-basement-core-lite-runtime" {
			continue
		}
		return contract.Installation{Domain: p.Network.Configuration.Domain.Base, SiteRef: a.Owner.Binding.SiteRef, NodeRef: a.Owner.Binding.NodeRef, CoreModuleRef: route.CoreModuleRef, TLSIssuerRef: route.TLS.IssuerRef, TLSProfileRef: route.TLS.ProfileRef, Exposure: route.Exposure}, nil
	}
	return contract.Installation{}, errors.New("own application requires an applied Direct Compose Home core with an existing TLS route")
}

func checkOwnApplicationCollisions(a nativeV2AppliedAuthority, name string) error {
	var p struct {
		Network struct {
			Routes []struct {
				ID   string `json:"id"`
				Host string `json:"host"`
			} `json:"routes"`
			Configuration struct {
				Domain struct {
					Base string `json:"base"`
				} `json:"domain"`
			} `json:"configuration"`
		} `json:"network"`
	}
	if err := json.Unmarshal(a.Plan.Canonical(), &p); err != nil {
		return err
	}
	ref := "own-" + name
	for _, workload := range a.AppliedWorkloads {
		if workload.WorkloadRef == ref {
			return errors.New("own application name collides with a catalog workload")
		}
	}
	for _, route := range p.Network.Routes {
		if route.ID == ref || route.Host == ref+"."+p.Network.Configuration.Domain.Base {
			return errors.New("own application hostname collides with an installation route")
		}
	}
	return nil
}

func requireOwnApplicationBackupCoverage(a nativeV2AppliedAuthority) error {
	_, requirement, err := nativeV2BackupPolicyRequirement(a.Plan, a.Owner.Binding.SiteRef, a.Owner.Binding.NodeRef)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(a.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	_, _, policy, err := readNativeV2BackupPolicyForArtifact(transaction, a.Manifest, requirement.ID)
	if err != nil {
		return err
	}
	return ownapplication.RequireBackupCoverage(policy)
}
