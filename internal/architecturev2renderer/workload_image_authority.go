package architecturev2renderer

// These aliases expose the generated CUE image authority to lifecycle adapters.
// Only the catalog maintains values; setup and execution consume the same
// compiled projection as rendering.
const (
	PrivateAIImageRef    = privateAIImageRef
	PrivateAIImageDigest = privateAIImageDigest
	PrivateAIRelease     = privateAIRelease
	OllamaImageRef       = ollamaImageRef
	OllamaImageDigest    = ollamaImageDigest
	OllamaRelease        = ollamaRelease

	HomeAssistantImageRef    = homeAssistantImageRef
	HomeAssistantImageDigest = homeAssistantImageDigest
	HomeAssistantRelease     = homeAssistantRelease
	JellyfinImageRef         = jellyfinImageRef
	JellyfinImageDigest      = jellyfinImageDigest
	JellyfinRelease          = jellyfinRelease
	VaultwardenImageRef      = vaultwardenImageRef
	VaultwardenImageDigest   = vaultwardenImageDigest
	VaultwardenRelease       = vaultwardenRelease
)

// Gitea image identity is derived from the same CUE catalog as deployment.
const (
	GiteaImageRef    = giteaImageRef
	GiteaImageDigest = giteaImageDigest
	GiteaRelease     = giteaRelease
)
