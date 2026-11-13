package nativehost

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ErrApplicationReleaseDigestMismatch reports a registry tag that no longer
// resolves to the digest the owner accepted.
var ErrApplicationReleaseDigestMismatch = errors.New("the registry resolves the release tag to another digest")

// ResolveApplicationReleaseDigest re-resolves one tag-pinned upstream release
// from its public HTTPS registry (the same address-admitted transport as own
// application images). The tag must resolve to exactly digest, and the
// manifest under that digest must carry an image for the node platform. It
// reads manifests and the image configuration; no layer is pulled.
func ResolveApplicationReleaseDigest(ctx context.Context, ref, digest, os, arch string) error {
	tagged, err := name.NewTag(ref, name.StrictValidation)
	if err != nil {
		return errors.New("release image reference is invalid")
	}
	pinned, err := name.NewDigest(tagged.Context().Name()+"@"+digest, name.StrictValidation)
	if err != nil {
		return errors.New("release image digest is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.DialContext = dialOwnRegistry
	defer transport.CloseIdleConnections()
	options := []remote.Option{remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithTransport(ownRegistryTransport{transport})}
	descriptor, err := remote.Get(tagged, options...)
	if err != nil {
		return fmt.Errorf("resolve %s from its registry: %w", ref, err)
	}
	if descriptor.Digest.String() != digest {
		return fmt.Errorf("%w: %s is %s, not %s", ErrApplicationReleaseDigestMismatch, ref, descriptor.Digest, digest)
	}
	// A single-platform manifest is returned whatever platform is asked for,
	// so the image configuration names the platform it actually runs on.
	image, err := remote.Image(pinned, append(options, remote.WithPlatform(v1.Platform{OS: os, Architecture: arch}))...)
	if err != nil {
		return fmt.Errorf("%s@%s carries no %s/%s image: %w", ref, digest, os, arch, err)
	}
	config, err := image.ConfigFile()
	if err != nil || config.OS != os || config.Architecture != arch {
		return fmt.Errorf("%s@%s carries no %s/%s image", ref, digest, os, arch)
	}
	return nil
}
