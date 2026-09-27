package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kombifyio/stackkits/cmd/stackkit/user"
	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type nativeFilesHouseholdProvisioner struct{}

func (nativeFilesHouseholdProvisioner) EnsureFilesAccount(ctx context.Context, workspace string, member localowner.HouseholdUser) (account user.FilesAccount, returnErr error) {
	err := withLifecycleMutation(workspace, "user files ensure", func() error {
		observed, err := ensureNativeFilesHouseholdAccount(ctx, workspace, member)
		account = observed
		return err
	})
	return account, err
}

func (nativeFilesHouseholdProvisioner) RevealFilesCredential(ctx context.Context, workspace string, member localowner.HouseholdUser) (material []byte, returnErr error) {
	err := withLifecycleMutation(workspace, "user files credential", func() error {
		account, err := ensureNativeFilesHouseholdAccount(ctx, workspace, member)
		if err != nil {
			return err
		}
		if !account.Selected {
			return errors.New("the current Plan does not select Cloudreve Files")
		}
		ref, err := appsetup.CloudreveHouseholdCredentialRef(member.Username, member.Email)
		if err != nil {
			return err
		}
		material, err = localevidence.ResolveLocalSecretMaterial(workspace, ref)
		if err != nil {
			return errors.New("the Files household credential is not available in valid local-owner custody")
		}
		return nil
	})
	if err != nil {
		clear(material)
		return nil, err
	}
	return material, nil
}

func ensureNativeFilesHouseholdAccount(ctx context.Context, workspace string, member localowner.HouseholdUser) (user.FilesAccount, error) {
	authority, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
	if err != nil {
		return user.FilesAccount{}, err
	}
	plan, err := resolvedplan.DecodeCanonicalPlan(authority.Plan.Canonical())
	if err != nil {
		return user.FilesAccount{}, err
	}
	contracts, err := applicationlifecycle.ContractsFromResolvedPlan(plan)
	if err != nil {
		return user.FilesAccount{}, err
	}
	var files *applicationlifecycle.Contract
	for i := range contracts {
		if contracts[i].WorkloadRef == "files" {
			candidate := contracts[i]
			files = &candidate
			break
		}
	}
	if files == nil {
		return user.FilesAccount{Selected: false}, nil
	}
	setup := architectureV2SetupInput(plan, *files, nil)
	if setup.Policy != "on-demand" || len(setup.ActionRefs) != 1 || setup.ActionRefs[0] != "cloudreve-owner-bootstrap" {
		return user.FilesAccount{}, errors.New("the selected Files workload has no supported Cloudreve owner authority")
	}
	deployment, err := nativeAppliedWorkloadDeployment(authority, "files")
	if err != nil {
		return user.FilesAccount{}, err
	}
	adapter, err := nativeApplicationSetupAdapter(deployment)
	if err != nil {
		return user.FilesAccount{}, err
	}
	description, supported := appsetup.DescribeNativeAction(setup.ActionRefs[0], adapter)
	if !supported {
		return user.FilesAccount{}, errors.New("the selected runtime adapter cannot provision app-local Files accounts")
	}
	if deployment.Release != appsetup.CloudrevePinnedVersion {
		return user.FilesAccount{}, errors.New("the applied Files version is outside the household provisioner contract")
	}

	ownerCredentials, err := readFilesOwnerCredentials(authority.WorkspaceRoot, description.CredentialsFile)
	if err != nil {
		return user.FilesAccount{}, err
	}
	defer func() { ownerCredentials.Password = "" }()
	if strings.TrimSpace(ownerCredentials.Email) == "" || strings.TrimSpace(ownerCredentials.Password) == "" {
		return user.FilesAccount{}, errors.New("the Files owner credential is unavailable for household provisioning")
	}

	ref, err := appsetup.CloudreveHouseholdCredentialRef(member.Username, member.Email)
	if err != nil {
		return user.FilesAccount{}, err
	}
	if err := localevidence.MaterializeLocalSecret(authority.WorkspaceRoot, ref); err != nil {
		return user.FilesAccount{}, fmt.Errorf("establish Files household credential custody: %w", err)
	}
	material, err := localevidence.ResolveLocalSecretMaterial(authority.WorkspaceRoot, ref)
	if err != nil {
		return user.FilesAccount{}, errors.New("the Files household credential custody is invalid")
	}
	defer clear(material)
	householdPassword := string(material)
	defer func() { householdPassword = "" }()

	var observed appsetup.CloudreveHouseholdResult
	err = adapter.WithHTTP(ctx, authority.WorkspaceRoot, deployment, func(client *http.Client, baseURL string) error {
		result, setupErr := appsetup.EnsureCloudreveHouseholdUser(ctx, client, baseURL, appsetup.CloudreveHouseholdRequest{
			OwnerEmail: ownerCredentials.Email, OwnerPassword: ownerCredentials.Password,
			Email: member.Email, Nickname: member.Username, Password: householdPassword,
			ExpectedVersion: deployment.Release,
		})
		observed = result
		return setupErr
	})
	if err != nil {
		return user.FilesAccount{}, err
	}
	if observed.UserID == "" || !strings.EqualFold(observed.Email, strings.TrimSpace(member.Email)) || observed.Nickname != strings.TrimSpace(member.Username) || observed.GroupID != 2 {
		return user.FilesAccount{}, errors.New("Cloudreve did not verify the exact non-admin household identity")
	}
	return user.FilesAccount{Selected: true, AccountRef: observed.UserID, Email: observed.Email}, nil
}
