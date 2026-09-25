package runtimeexecutoropentofu

import "github.com/kombifyio/stackkits/internal/architecturev2renderer"

// rootComposePayload returns the Compose payload a Stage 1 wrapper root
// writes (architecturev2renderer.ExtractComposePayload). The executor hands
// it to the native side steps before apply and proves after apply that the
// runtime Compose file holds exactly these bytes.
func rootComposePayload(root []byte) ([]byte, error) {
	return architecturev2renderer.ExtractComposePayload(root)
}
