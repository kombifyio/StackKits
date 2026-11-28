package commands

import (
	"fmt"
	"net/http"

	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

func newAddressCookiePolicyCommand() *cobra.Command {
	var zone, base string
	command := &cobra.Command{Use: "cookie-policy", Short: "Describe the installation's cookie scope without issuing credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		kind, ok := map[string]stackspecintent.AddressZoneKind{"custom": stackspecintent.AddressZoneCustomDomain, "lan": stackspecintent.AddressZoneLocalOnly, "kombify-me": stackspecintent.AddressZoneKombifyMe}[zone]
		if !ok {
			return fmt.Errorf("expected --zone custom, lan, or kombify-me")
		}
		contract, err := stackspecintent.ResolveAddressZone(stackspecintent.AddressZoneOptions{ZoneKind: kind, Domain: base})
		if err != nil {
			return err
		}
		headers := []string{}
		for _, name := range []string{"__Host-stackkit-session", "__Host-stackkit-launch", contract.CookiePrefix + "stackkit-sso"} {
			cookie := http.Cookie{Name: name, Value: "example", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
			if name == "__Secure-stackkit-sso" && contract.CookieMode == stackspecintent.CookieScopeZoneParent {
				cookie.Domain = contract.Zone
			}
			headers = append(headers, cookie.String())
		}
		return writeCommandResult(cmd, "address cookie-policy", map[string]any{"zone": zone, "base": contract.Zone, "setCookies": headers})
	}}
	command.Flags().StringVar(&zone, "zone", "", "Address zone kind")
	command.Flags().StringVar(&base, "base", "", "Installation base domain")
	command.Flags().Bool("json", false, "Emit machine-readable policy")
	_ = command.MarkFlagRequired("base")
	return command
}
