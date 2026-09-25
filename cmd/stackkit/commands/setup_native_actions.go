package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

type nativeOwnerSetupObservation struct {
	AccountRef         string
	Initialized        bool
	AdminLoginVerified bool
	OnboardingComplete bool
	Preparation        string
}

func validateNativeOwnerSetupAction(deployment runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, action string, options nativeSetupOptions) error {
	switch action {
	case "jellyfin-owner-bootstrap":
		_, err := architecturev2renderer.ParseJellyfinWorkloadBundle(deployment.Bundle)
		return err
	case "cloudreve-owner-bootstrap":
		if options.completeOnboarding {
			return errors.New("Files storage and sharing settings belong to the owner; omit --complete-onboarding to verify the administrator account")
		}
		_, err := architecturev2renderer.ParseCloudreveWorkloadBundle(deployment.Bundle)
		return err
	case "immich-owner-bootstrap":
		_, err := architecturev2renderer.ParseImmichWorkloadBundle(deployment.Bundle)
		return err
	case "home-assistant-owner-bootstrap":
		if options.completeOnboarding {
			return errors.New("Home Assistant personal onboarding settings must be completed in the application; omit --complete-onboarding to verify the owner account")
		}
		_, err := architecturev2renderer.ParseHomeAssistantWorkloadBundle(deployment.Bundle)
		return err
	case "pterodactyl-game-server-setup":
		if options.completeOnboarding {
			return errors.New("game server setup has no separate onboarding; omit --complete-onboarding")
		}
		_, err := architecturev2renderer.ParsePterodactylWorkloadBundle(deployment.Bundle)
		return err
	case "roundcube-mailbox-login":
		if options.completeOnboarding {
			return errors.New("the mailbox login check has no separate onboarding; omit --complete-onboarding")
		}
		_, err := architecturev2renderer.ParseRoundcubeWorkloadBundle(deployment.Bundle)
		return err
	case applicationlifecycle.VaultOwnerInviteActionRef:
		if options.completeOnboarding {
			return errors.New("Vaultwarden personal encryption setup must be completed in the official client; omit --complete-onboarding")
		}
		_, err := architecturev2renderer.ParseVaultwardenWorkloadBundle(deployment.Bundle)
		return err
	default:
		return errors.New("the declared application setup action is not implemented")
	}
}

func executeNativeOwnerSetupAction(ctx context.Context, client *http.Client, baseURL, workspace string, deployment runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, release, action string, options nativeSetupOptions) (observation nativeOwnerSetupObservation, returnErr error) {
	switch action {
	case "jellyfin-owner-bootstrap":
		var credentials struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() { credentials.Password = "" }()
		observed, err := appsetup.BootstrapJellyfinOwner(ctx, client, baseURL, appsetup.JellyfinOwnerRequest{
			Username: credentials.Username, Password: credentials.Password, ExpectedVersion: release,
			CompleteOnboarding: options.completeOnboarding,
		})
		if err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() {
			if cleanupErr := observed.Cleanup(context.Background(), client, baseURL); cleanupErr != nil {
				observation = nativeOwnerSetupObservation{}
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}()
		if !observed.UserIsAdmin || observed.Version != release {
			return nativeOwnerSetupObservation{}, errors.New("Jellyfin did not verify the administrator of the admitted application version")
		}
		return nativeOwnerSetupObservation{AccountRef: observed.UserID, Initialized: true, AdminLoginVerified: observed.UserIsAdmin, OnboardingComplete: observed.StartupWizardCompleted}, nil
	case "cloudreve-owner-bootstrap":
		var credentials struct {
			Email                       string `json:"email"`
			Password                    string `json:"password"`
			Language                    string `json:"language"`
			AllowFirstOwnerRegistration bool   `json:"allowFirstOwnerRegistration"`
		}
		if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() { credentials.Password = "" }()
		observed, err := appsetup.BootstrapCloudreveOwner(ctx, client, baseURL, appsetup.CloudreveOwnerRequest{
			Email: credentials.Email, Password: credentials.Password, Language: credentials.Language, ExpectedVersion: release,
			AllowFirstOwnerRegistration: credentials.AllowFirstOwnerRegistration,
		})
		if err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() {
			if cleanupErr := observed.Cleanup(context.Background(), client, baseURL); cleanupErr != nil {
				observation = nativeOwnerSetupObservation{}
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}()
		if !observed.UserIsAdmin || observed.Version != release {
			return nativeOwnerSetupObservation{}, errors.New("Cloudreve did not verify the administrator of the admitted application version")
		}
		return nativeOwnerSetupObservation{AccountRef: observed.UserID, Initialized: observed.ServerInitialized, AdminLoginVerified: observed.UserIsAdmin, OnboardingComplete: observed.ServerInitialized && observed.UserIsAdmin}, nil
	case "immich-owner-bootstrap":
		var credentials struct {
			Email       string `json:"email"`
			Password    string `json:"password"`
			DisplayName string `json:"displayName"`
		}
		if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() { credentials.Password = "" }()
		var version struct{ Major, Minor, Patch int }
		if err := appsetup.ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/server/version", nil, "", &version); err != nil {
			return nativeOwnerSetupObservation{}, errors.New("could not verify the applied Immich API version before setup")
		}
		if fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch) != release {
			return nativeOwnerSetupObservation{}, errors.New("running Immich API version differs from the admitted workload")
		}
		observed, err := appsetup.BootstrapImmichOwner(ctx, client, baseURL, appsetup.ImmichOwnerRequest{
			Email: credentials.Email, Password: credentials.Password, DisplayName: credentials.DisplayName,
			CompleteOnboarding: options.completeOnboarding,
		})
		if err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() {
			if cleanupErr := observed.Cleanup(context.Background(), client, baseURL); cleanupErr != nil {
				observation = nativeOwnerSetupObservation{}
				if returnErr == nil {
					returnErr = cleanupErr
				} else {
					returnErr = errors.Join(returnErr, cleanupErr)
				}
			}
		}()
		var finalVersion struct{ Major, Minor, Patch int }
		if err := appsetup.ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/server/version", nil, "", &finalVersion); err != nil || fmt.Sprintf("v%d.%d.%d", finalVersion.Major, finalVersion.Minor, finalVersion.Patch) != release {
			return nativeOwnerSetupObservation{}, errors.New("running Immich API version changed before setup result verification")
		}
		return nativeOwnerSetupObservation{AccountRef: observed.UserID, Initialized: observed.ServerInitialized, AdminLoginVerified: observed.UserIsAdmin, OnboardingComplete: observed.OnboardingComplete}, nil
	case "home-assistant-owner-bootstrap":
		var credentials struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			DisplayName string `json:"displayName"`
			Language    string `json:"language"`
		}
		if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() { credentials.Password = "" }()
		observed, err := appsetup.BootstrapHomeAssistantOwner(ctx, client, baseURL, appsetup.HomeAssistantOwnerRequest{
			Username: credentials.Username, Password: credentials.Password, DisplayName: credentials.DisplayName,
			Language: credentials.Language, ExpectedVersion: release,
		})
		if err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		if !observed.UserIsOwner || !observed.UserIsAdmin || observed.Version != release {
			return nativeOwnerSetupObservation{}, errors.New("Home Assistant did not verify the owner of the admitted application version")
		}
		return nativeOwnerSetupObservation{AccountRef: observed.UserID, Initialized: observed.ServerInitialized, AdminLoginVerified: observed.UserIsOwner && observed.UserIsAdmin, OnboardingComplete: observed.OnboardingComplete}, nil
	case "pterodactyl-game-server-setup":
		return executePterodactylGameServerSetup(ctx, client, baseURL, workspace, deployment, release, options)
	case "roundcube-mailbox-login":
		return executeRoundcubeMailboxSetup(ctx, client, baseURL, workspace, deployment, release, options)
	case applicationlifecycle.VaultOwnerInviteActionRef:
		var credentials struct {
			Email string `json:"email"`
		}
		if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		descriptor, err := architecturev2renderer.ParseVaultwardenWorkloadBundle(deployment.Bundle)
		if err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		adminToken, err := localevidence.ResolveLocalSecretMaterial(workspace, descriptor.AdminTokenRef)
		if err != nil {
			return nativeOwnerSetupObservation{}, errors.New("resolve the owner-custodied Vaultwarden admin token before setup")
		}
		defer clear(adminToken)
		observed, err := appsetup.BootstrapVaultwardenOwner(ctx, client, baseURL, appsetup.VaultwardenOwnerRequest{
			Email: credentials.Email, AdminToken: adminToken, ExpectedVersion: release,
		})
		if err != nil {
			return nativeOwnerSetupObservation{}, err
		}
		defer func() {
			if cleanupErr := observed.Cleanup(context.Background(), client, baseURL); cleanupErr != nil {
				observation = nativeOwnerSetupObservation{}
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}()
		if !observed.AdminLoginVerified || observed.Version != release || observed.UserID == "" || observed.Preparation == "" {
			return nativeOwnerSetupObservation{}, errors.New("Vaultwarden did not verify the admitted administrator invitation state")
		}
		return nativeOwnerSetupObservation{
			AccountRef: observed.UserID, AdminLoginVerified: true, Preparation: observed.Preparation,
		}, nil
	default:
		return nativeOwnerSetupObservation{}, errors.New("the declared application setup action is not implemented")
	}
}

// executePterodactylGameServerSetup creates one curated game server through
// the Panel APIs with keys derived from owner custody (ADR-0043). The owner's
// EULA acceptance is part of the private input and never assumed.
func executePterodactylGameServerSetup(ctx context.Context, client *http.Client, baseURL, workspace string, deployment runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, release string, options nativeSetupOptions) (nativeOwnerSetupObservation, error) {
	var credentials struct {
		Profile    string   `json:"profile"`
		Name       string   `json:"name"`
		AcceptEULA bool     `json:"acceptEula"`
		AllowList  []string `json:"allowList"`
		Password   string   `json:"password"`
	}
	if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	applicationKey, clientKey, err := pterodactylCustodyKeys(workspace, deployment)
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return nativeOwnerSetupObservation{}, fmt.Errorf("resolve the workload owner identity: %w", err)
	}
	result, err := appsetup.CreatePterodactylGameServer(ctx, client, baseURL, appsetup.GameServerRequest{
		Profile: credentials.Profile, Name: credentials.Name, AcceptEULA: credentials.AcceptEULA, AllowList: credentials.AllowList,
		Password: credentials.Password, OwnerEmail: owner.PocketID.Email, ApplicationKey: applicationKey, ClientKey: clientKey, ExpectedVersion: release,
	})
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	printInfo("Game server %s is ready on port %d/%s; join check: %s", result.Identifier, result.Port, result.Protocol, result.Reachable)
	return nativeOwnerSetupObservation{AccountRef: result.ServerUUID, Initialized: true, AdminLoginVerified: true, OnboardingComplete: true}, nil
}

// pterodactylCustodyKeys derives the Panel's Application and Client API keys
// from owner custody exactly as the bootstrap minted them (ADR-0043).
func pterodactylCustodyKeys(workspace string, deployment runtimeexecutorlocal.SelectedPaaSWorkloadDeployment) (string, string, error) {
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
	if err != nil {
		return "", "", err
	}
	derive := func(slot, prefix string) (string, error) {
		material, err := localevidence.ResolveLocalSecretMaterial(workspace, bundle.SecretRefs[slot])
		if err != nil {
			return "", fmt.Errorf("resolve the owner-custodied %s: %w", slot, err)
		}
		defer clear(material)
		if len(material) < 43 {
			return "", fmt.Errorf("custody material for %s is too short", slot)
		}
		return prefix + string(material[:11]) + string(material[11:43]), nil
	}
	applicationKey, err := derive("application-api-key", "ptla_")
	if err != nil {
		return "", "", err
	}
	clientKey, err := derive("client-api-key", "ptlc_")
	if err != nil {
		return "", "", err
	}
	return applicationKey, clientKey, nil
}

// executeRoundcubeMailboxSetup stores the owner's IMAP and SMTP endpoints on
// the Mail workload's persistent mailbox volume (never the password), then
// verifies a real login through Roundcube's own form. A failed login restores
// the previously active endpoints, so a typo never replaces a working setup.
func executeRoundcubeMailboxSetup(ctx context.Context, client *http.Client, baseURL, workspace string, deployment runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, release string, options nativeSetupOptions) (nativeOwnerSetupObservation, error) {
	var credentials struct {
		IMAPHost     string `json:"imapHost"`
		IMAPPort     int    `json:"imapPort"`
		IMAPSecurity string `json:"imapSecurity"`
		SMTPHost     string `json:"smtpHost"`
		SMTPPort     int    `json:"smtpPort"`
		SMTPSecurity string `json:"smtpSecurity"`
		Username     string `json:"username"`
		Password     string `json:"password"`
	}
	if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	defer func() { credentials.Password = "" }()
	imap, err := appsetup.ResolveRoundcubeMailboxEndpoint("IMAP", credentials.IMAPHost, credentials.IMAPPort, credentials.IMAPSecurity)
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	smtp, err := appsetup.ResolveRoundcubeMailboxEndpoint("SMTP", credentials.SMTPHost, credentials.SMTPPort, credentials.SMTPSecurity)
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	if err := runtimeexecutorlocal.ConfigureStandaloneComposeRoundcubeMailbox(ctx, workspace, deployment, imap.URI(), smtp.URI()); err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	observed, err := appsetup.VerifyRoundcubeMailboxLogin(ctx, client, baseURL, appsetup.RoundcubeMailboxLoginRequest{
		IMAP: imap, Username: credentials.Username, Password: credentials.Password, ExpectedRelease: release,
	})
	if err != nil {
		if revertErr := runtimeexecutorlocal.RevertStandaloneComposeRoundcubeMailbox(context.WithoutCancel(ctx), workspace, deployment); revertErr != nil {
			return nativeOwnerSetupObservation{}, errors.Join(err, fmt.Errorf("restore the previous mailbox endpoints: %w", revertErr))
		}
		return nativeOwnerSetupObservation{}, err
	}
	printInfo("Mail is set up: IMAP %s, SMTP %s. Device setup: see the mail-client guide.", imap.URI(), smtp.URI())
	return nativeOwnerSetupObservation{AccountRef: observed.MailboxRef, Initialized: true, AdminLoginVerified: true}, nil
}
