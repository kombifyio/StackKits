package appsetup

// NativeActionDescription is the user-facing metadata for a native setup
// action. It is shared by status surfaces so they do not maintain their own
// application or credential lists.
type NativeActionDescription struct {
	Title                        string
	CredentialFields             []string
	CredentialsFile              string
	GuideURL                     string
	SupportsOnboardingCompletion bool
}

// DescribeNativeAction returns the bounded native setup contract for an
// action and adapter pair.
func DescribeNativeAction(action, adapter string) (NativeActionDescription, bool) {
	if adapter != "standalone-compose" {
		return NativeActionDescription{}, false
	}
	switch action {
	case "jellyfin-owner-bootstrap":
		return NativeActionDescription{
			Title:                        "Media owner setup",
			CredentialFields:             []string{"username", "password"},
			CredentialsFile:              ".stackkit/setup/media-owner.json",
			GuideURL:                     "https://github.com/kombifyio/stackKits/blob/main/use-cases/media/agent/owner-setup/SKILL.md#owner-setup",
			SupportsOnboardingCompletion: true,
		}, true
	case "cloudreve-owner-bootstrap":
		return NativeActionDescription{
			Title:                        "Files owner setup",
			CredentialFields:             []string{"email", "password"},
			CredentialsFile:              ".stackkit/setup/files-owner.json",
			GuideURL:                     "https://github.com/kombifyio/stackKits/blob/main/use-cases/files/agent/owner-setup/SKILL.md#owner-setup",
			SupportsOnboardingCompletion: false,
		}, true
	case "immich-owner-bootstrap":
		return NativeActionDescription{
			Title:                        "Photos owner setup",
			CredentialFields:             []string{"email", "password", "displayName"},
			CredentialsFile:              ".stackkit/setup/owner.json",
			GuideURL:                     "https://github.com/kombifyio/stackKits/blob/main/use-cases/photos/agent/family-vault/SKILL.md#owner-setup",
			SupportsOnboardingCompletion: true,
		}, true
	case "home-assistant-owner-bootstrap":
		return NativeActionDescription{
			Title:                        "Smart Home owner setup",
			CredentialFields:             []string{"username", "password", "displayName"},
			CredentialsFile:              ".stackkit/setup/home-assistant-owner.json",
			GuideURL:                     "https://github.com/kombifyio/stackKits/blob/main/use-cases/smart-home/agent/homelab-mcp/SKILL.md#owner-setup",
			SupportsOnboardingCompletion: false,
		}, true
	case "vault-owner-invite":
		return NativeActionDescription{
			Title:                        "Vault owner invitation",
			CredentialFields:             []string{"email"},
			CredentialsFile:              ".stackkit/setup/vault-owner.json",
			GuideURL:                     "https://github.com/kombifyio/stackKits/blob/main/use-cases/vault/agent/owner-setup/SKILL.md#owner-setup",
			SupportsOnboardingCompletion: false,
		}, true
	default:
		return NativeActionDescription{}, false
	}
}

// SupportsNativeAction reports executable adapter support, separately from
// CUE's declared application intent. Unsupported adapters never fall back.
func SupportsNativeAction(action, adapter string) bool {
	_, supported := DescribeNativeAction(action, adapter)
	return supported
}
