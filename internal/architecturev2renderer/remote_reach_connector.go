package architecturev2renderer

// RemoteReachConnectorImage is the pinned cloudflared image (ref@digest) of
// the remote reach connector that carries kombify.me tunnel mode or the
// Owner's own Cloudflare Tunnel to the remote listener (homelab identity
// target §8). It is the catalog's Cloudflare Tunnel connector pin, the same
// upstream image as the kombify AI connector, so one pin moves both.
func RemoteReachConnectorImage() string {
	return kombifyAIConnectorImageRef + "@" + kombifyAIConnectorImageDigest
}
