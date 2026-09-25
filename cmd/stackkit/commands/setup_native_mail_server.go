package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// executeStalwartMailDomainSetup creates the owner's mail domain and first
// mailbox with the custody-held Stalwart administrator, prints the DNS
// records the owner publishes and verifies IMAPS and submission on the node
// (ADR-0046). The mailbox password is read from the private credentials file,
// used for this action only and never stored or logged.
func executeStalwartMailDomainSetup(ctx context.Context, client *http.Client, baseURL, workspace string, deployment nativehost.SelectedPaaSWorkloadDeployment, options nativeSetupOptions) (nativeOwnerSetupObservation, error) {
	var credentials struct {
		Domain             string `json:"domain"`
		LocalPart          string `json:"localPart"`
		Password           string `json:"password"`
		RequestCertificate *bool  `json:"requestCertificate"`
		// Relay is the optional outbound smarthost; its password reaches
		// Stalwart once and is never logged or stored by StackKits.
		Relay *struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Security string `json:"security"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"relay"`
	}
	if err := readNativeSetupCredentialJSON(workspace, options.credentialsFile, &credentials); err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	var relay *appsetup.StalwartRelay
	if credentials.Relay != nil {
		relay = &appsetup.StalwartRelay{
			Host: credentials.Relay.Host, Port: credentials.Relay.Port, Security: credentials.Relay.Security,
			Username: credentials.Relay.Username, Password: credentials.Relay.Password,
		}
	}
	defer func() {
		credentials.Password = ""
		if credentials.Relay != nil {
			credentials.Relay.Password = ""
		}
		if relay != nil {
			relay.Password = ""
		}
	}()
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
		Relay: relay,
	})
	if err != nil {
		return nativeOwnerSetupObservation{}, err
	}
	printInfo("Mailbox %s is ready on %s (IMAPS 993, submission 587 with STARTTLS, submissions 465).", result.Address, descriptor.MailHost)
	printInfo("Outbound mail: %s.", result.Outbound)
	printInfo("Certificate: %s. Trusted by this node now: %t.", result.Certificate, result.CertificateTrusted)
	printInfo("Publish these DNS records at your DNS provider; StackKits never changes DNS:")
	for _, record := range result.Records {
		need := "optional"
		if record.Required {
			need = "required"
		}
		printInfo("  %s %s %s    ; %s, %s", record.Name, record.Type, record.Value, need, record.Purpose)
	}
	ipv4 := strings.Join(result.PublicIPv4, ", ")
	printInfo("This node is a dedicated mail node: keep %s pointing at its fixed public IPv4 %s and set the reverse DNS (PTR) of %s to %s at your server provider.", descriptor.MailHost, ipv4, ipv4, descriptor.MailHost)
	printInfo("The node's firewall must admit TCP 25, 465, 587, 993, 4190 and 443. Some providers block port 25 until you ask them to lift it.")
	if relay != nil {
		printInfo("Mail now leaves through your relay: add the relay provider's SPF include to the domain's SPF record.")
	}
	if !result.IMAPLoginVerified || !result.SubmissionVerified {
		return nativeOwnerSetupObservation{}, fmt.Errorf("the mailbox checks did not complete")
	}
	return nativeOwnerSetupObservation{AccountRef: result.MailboxRef, Initialized: true, AdminLoginVerified: true, OnboardingComplete: true}, nil
}
