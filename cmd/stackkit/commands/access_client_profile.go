package commands

import (
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

//go:embed access_client_profile_linux.sh
var accessClientLinuxAdapter string

func clientProfileDiagnosticShell() (string, error) {
	var handler strings.Builder
	handler.WriteString("fail() {\ncase \"$1\" in\n")
	for _, code := range []string{"client_trust_approval_required", "client_trust_browser_user_missing", "client_trust_adapter_unavailable", "client_trust_dns_upstream_unsupported", "client_trust_ca_mismatch", "client_trust_local_conflict", "client_trust_profile_missing", "unknown_failure"} {
		diagnostic, ok := actionableerror.Diagnostic(code)
		if !ok {
			return "", fmt.Errorf("client trust diagnostic %s is not registered", code)
		}
		message := code + ": " + diagnostic.Message + " Next: " + strings.Join(diagnostic.UserGuidance, " ")
		pattern := code
		if code == "unknown_failure" {
			pattern = "*"
		}
		fmt.Fprintf(&handler, "%s) printf '%%s\\n' %s >&2 ;;\n", pattern, remoteShellQuote(message))
	}
	handler.WriteString("esac\n[ -z \"${2:-}\" ] || printf 'Context: %s\\n' \"$2\" >&2\nexit 2\n}\n")
	return handler.String(), nil
}

func init() {
	cmd := &cobra.Command{
		Use: "client-trust", Short: "Verify public Owner-CA and scoped DNS custody for a client",
		Long:        "Verify the signed public CA and LAN resolver handoff on this installed host. With --export, prepare the Linux OS trust adapter. This does not verify the application runtime, approve client trust, issue device credentials or establish a service session.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	var export, asJSON bool
	cmd.Flags().BoolVar(&export, "export", false, "Export .stackkit/client-trust/enroll-linux.sh from authenticated public custody")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the prepared public trust handoff as JSON")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		wd := getWorkDir()
		custody, err := localevidence.LoadBasementRuntimeCustody(wd)
		if err != nil {
			return machineAwareCommandError(cmd, actionableerror.WithDiagnostic("client_trust_profile_invalid", err))
		}
		access := &accessSummary{Domain: custody.Domain}
		attachAccessClientTrust(wd, access)
		if access.ClientTrust == nil || !strings.Contains(custody.Domain, ".") {
			return machineAwareCommandError(cmd, actionableerror.WithDiagnostic("client_trust_profile_invalid", errors.New("private-zone public custody is unavailable")))
		}
		access.ClientTrust.RemoteAccess.Status = "unverified"
		profile := ""
		if export {
			if err := writeAccessClientProfile(wd, access); err != nil {
				if _, known := actionableerror.FromError(err); !known {
					err = actionableerror.WithDiagnostic("client_trust_profile_invalid", err)
				}
				return machineAwareCommandError(cmd, err)
			}
			profile = access.ClientTrust.LinuxProfilePath
		}
		report := struct {
			SchemaVersion       string             `json:"schemaVersion"`
			Status              string             `json:"status"`
			RuntimeVerification string             `json:"runtimeVerification"`
			ClientApproval      string             `json:"clientApproval"`
			ProfilePath         string             `json:"profilePath,omitempty"`
			ClientTrust         *accessClientTrust `json:"clientTrust"`
		}{"stackkit.client-trust-handoff/v1", "prepared", "not-requested", "pending", profile, access.ClientTrust}
		if asJSON {
			return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", report)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Public Owner-CA custody verified for %s; resolver %s; CA %s.\nRuntime verification: not requested. Client OS approval: pending.\n", custody.Domain, access.ClientTrust.ResolverAddress, access.ClientTrust.CAFingerprint)
		if export {
			fmt.Fprintf(cmd.OutOrStdout(), "Profile prepared: %s. Copy through trusted server access, review the CA fingerprint, then approve on the client with sudo sh enroll-linux.sh --approve.\n", filepath.Join(wd, profile))
		}
		return nil
	}
	verifyCmd.AddCommand(cmd)
}

// writeAccessClientProfile exports only public trust from authenticated local
// custody. Access JSON is advisory and cannot supply a substitute trust root.
func writeAccessClientProfile(wd string, summary *accessSummary) error {
	if summary.ClientTrust == nil {
		return nil
	}
	trust := summary.ClientTrust
	custody, err := localevidence.LoadBasementRuntimeCustody(wd)
	if err != nil {
		return fmt.Errorf("client_trust_profile_invalid: read authenticated runtime custody: %w", err)
	}
	if !canonicalPrivateZone(summary.Domain) || !strings.Contains(summary.Domain, ".") || trust.CanonicalZone != summary.Domain || custody.Domain != summary.Domain {
		return errors.New("client_trust_profile_invalid: private zone differs from the access manifest")
	}
	address, err := localevidence.BasementLANDNSResolverAddress(wd)
	if err != nil || address != trust.ResolverAddress {
		return errors.New("client_trust_profile_invalid: resolver differs from signed runtime custody")
	}
	resolverIP, err := netip.ParseAddr(address)
	if err != nil || resolverIP.Zone() != "" || resolverIP.IsLoopback() || !resolverIP.IsGlobalUnicast() {
		return errors.New("client_trust_profile_invalid: resolver must be a site IP address")
	}
	raw, _, err := localevidence.BasementStepCARootCAPEM(wd)
	if err != nil {
		return fmt.Errorf("client_trust_profile_invalid: read authenticated public Owner CA: %w", err)
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("client_trust_profile_invalid: expected one public CA certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA {
		return errors.New("client_trust_profile_invalid: invalid public Owner CA")
	}
	if certificate.PublicKeyAlgorithm == x509.Ed25519 {
		return actionableerror.WithDiagnostic("client_trust_browser_ca_unsupported", errors.New("browser-facing CA requires a supported X.509 algorithm"))
	}
	digest := sha256.Sum256(certificate.Raw)
	hexDigest := hex.EncodeToString(digest[:])
	if strings.ReplaceAll(strings.TrimPrefix(trust.CAFingerprint, "SHA256:"), ":", "") != strings.ToUpper(hexDigest) {
		return errors.New("client_trust_profile_invalid: fingerprint differs from signed Owner custody")
	}
	// canonicalPrivateZone alone accepts the private-zone suffix; shell values
	// additionally reject every character outside a DNS label.
	for _, label := range strings.Split(summary.Domain, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("client_trust_profile_invalid: invalid private DNS label")
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return errors.New("client_trust_profile_invalid: invalid private DNS label")
			}
		}
	}
	zoneDigest := sha256.Sum256([]byte(summary.Domain))
	id := hexDigest[:16] + "-" + hex.EncodeToString(zoneDigest[:4])
	header := fmt.Sprintf("#!/bin/sh\n# Public StackKits client trust; no account or device credential.\nset -eu\nZONE='%s'\nRESOLVER='%s'\nCA_SHA256='%s'\nPROFILE_ID='%s'\n", summary.Domain, address, hexDigest, id)
	diagnostics, err := clientProfileDiagnosticShell()
	if err != nil {
		return err
	}
	body := header + diagnostics + "OWNER_CA=$(cat <<'STACKKIT_PUBLIC_CA'\n" + string(pem.EncodeToMemory(block)) + "STACKKIT_PUBLIC_CA\n)\n" + accessClientLinuxAdapter
	dir := filepath.Join(wd, ".stackkit", "client-trust")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create public client trust profile: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".enroll-linux-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.WriteString(body); err == nil {
		err = temp.Chmod(0700)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write public client trust profile: %w", err)
	}
	if err := os.Rename(temp.Name(), filepath.Join(dir, "enroll-linux.sh")); err != nil {
		return fmt.Errorf("publish public client trust profile: %w", err)
	}
	return nil
}
