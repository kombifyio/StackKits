package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

// executeStalwartMailDomainSetup creates the owner's mail domain and first
// mailbox with the custody-held Stalwart administrator, prints the DNS
// records the owner publishes and verifies IMAPS and submission on the node
// (ADR-0046). The mailbox password is read from the private credentials file,
// used for this action only and never stored or logged.
func executeStalwartMailDomainSetup(ctx context.Context, client *http.Client, baseURL, workspace string, deployment runtimeexecutorlocal.SelectedPaaSWorkloadDeployment, options nativeSetupOptions) (nativeOwnerSetupObservation, error) {
	var credentials struct {
		Domain             string `json:"domain"`
		LocalPart          string `json:"localPart"`
		Password           string `json:"password"`
		RequestCertificate *bool  `json:"requestCertificate"`
	}
	if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	defer func() { credentials.Password = "" }()
	descriptor, err := architecturev2renderer.ParseStalwartWorkloadBundle(deployment.Bundle)
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	adminPassword, err := localevidence.ResolveLocalSecretMaterial(workspace, descriptor.AdminPasswordRef)
	if err != nil {
		return nativeOwnerSetupObservation{}, errors.New("resolve the owner-custodied Stalwart administrator password before setup")
	}
	defer clear(adminPassword)
	contact := ""
	if owner, err := localevidence.LoadOwnerCustody(workspace); err == nil && !localevidence.IsPlaceholderOwnerEmail(owner.PocketID.Email) {
		contact = owner.PocketID.Email
	}
	requestCertificate := credentials.RequestCertificate == nil || *credentials.RequestCertificate
	result, err := appsetup.SetupStalwartMailDomain(ctx, client, baseURL, appsetup.StalwartMailDomainRequest{
		Domain: credentials.Domain, LocalPart: credentials.LocalPart, Password: credentials.Password,
		MailHost: descriptor.MailHost, AdminPassword: adminPassword, ACMEContact: contact, RequestCertificate: requestCertificate,
	})
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	printInfo("Mailbox %s is ready on %s (IMAPS 993, submission 587 with STARTTLS, submissions 465).", result.Address, descriptor.MailHost)
	printInfo("Certificate: %s. Trusted by this node now: %t.", result.Certificate, result.CertificateTrusted)
	printInfo("Publish these DNS records at your DNS provider; StackKits never changes DNS:")
	for _, record := range result.Records {
		need := "optional"
		if record.Required {
			need = "required"
		}
		printInfo("  %s %s %s    ; %s, %s", record.Name, record.Type, record.Value, need, record.Purpose)
	}
	printInfo("Also keep %s pointing at this node and set its reverse DNS (PTR) to %s at your server provider.", descriptor.MailHost, descriptor.MailHost)
	if !result.IMAPLoginVerified || !result.SubmissionVerified {
		return nativeOwnerSetupObservation{}, fmt.Errorf("the mailbox checks did not complete")
	}
	return nativeOwnerSetupObservation{AccountRef: result.MailboxRef, Initialized: true, AdminLoginVerified: true, OnboardingComplete: true}, nil
}
