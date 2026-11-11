package nativehost

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/kombifyio/stackkits/contracts/ownapplication"
)

// ResolveOwnApplicationImage observes only a public HTTPS registry manifest,
// before approval. It pulls no layers and starts no daemon or container.
func ResolveOwnApplicationImage(ctx context.Context, image string) (string, error) {
	_, digest, e := ownapplication.SplitImage(image)
	if e != nil {
		return "", e
	}
	if digest != "" {
		return image, nil
	}
	ref, e := name.ParseReference(image, name.StrictValidation)
	if e != nil {
		return "", errors.New("image registry reference is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // A proxy must not bypass address admission.
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.DialContext = dialOwnRegistry
	defer transport.CloseIdleConnections()
	manifest, e := remote.Get(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithTransport(ownRegistryTransport{transport}))
	if e != nil {
		return "", errors.New("public HTTPS registry manifest resolution failed; supply an accessible image or its immutable digest")
	}
	repository, _, _ := ownapplication.SplitImage(image)
	repository = repository[:strings.LastIndex(repository, ":")]
	pinned := repository + "@" + manifest.Digest.String()
	if _, d, e := ownapplication.SplitImage(pinned); e != nil || d == "" {
		return "", errors.New("registry did not return an admitted SHA-256 manifest")
	}
	return pinned, nil
}

type ownRegistryTransport struct{ next http.RoundTripper }

func (t ownRegistryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Applies to manifests, bearer-token realms and every redirect; the library's
	// automatic plain HTTP fallback is rejected before any dial.
	if r.URL.Scheme != "https" || r.URL.User != nil || ownapplication.ValidateRegistry(r.URL.Host) != nil {
		return nil, errors.New("registry transport requires public DNS over HTTPS port 443")
	}
	return t.next.RoundTrip(r)
}

func dialOwnRegistry(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil || port != "443" || ownapplication.ValidateRegistry(host) != nil {
		return nil, errors.New("registry dial address is not admitted")
	}
	addresses, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if e != nil || len(addresses) == 0 {
		return nil, errors.New("public registry DNS unavailable")
	}
	for _, ip := range addresses {
		if !publicRegistryAddress(ip) {
			return nil, errors.New("registry DNS resolves to a non-public address")
		}
	}
	// Dial the already checked address, never resolve the hostname a second time.
	var last error
	for _, ip := range addresses {
		conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}

func publicRegistryAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// IANA special-use ranges, including CGNAT, documentation, benchmarking,
	// transition prefixes and IPv6 translation paths to private IPv4 targets.
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20", "5f00::/16"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return ip.Is4() || netip.MustParsePrefix("2000::/3").Contains(ip)
}

// AdmitImage fetches the approved manifest and every blob through the same
// public-address HTTPS transport. The daemon only imports a private archive;
// it never follows user-selected registry URLs or pulls an image itself.
func (r *OwnApplicationRuntime) AdmitImage(ctx context.Context, b ownapplication.Bundle) (ownapplication.Bundle, error) {
	phase := "reference"
	fail := func() (ownapplication.Bundle, error) {
		return b, errors.New("secure import of approved own application image failed at " + phase)
	}
	if _, d, e := ownapplication.SplitImage(b.ResolvedImage); e != nil || d == "" {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.DialContext = dialOwnRegistry
	defer transport.CloseIdleConnections()
	ref, e := name.ParseReference(b.ResolvedImage, name.StrictValidation)
	if e != nil {
		return fail()
	}
	phase = "manifest"
	image, e := remote.Image(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithTransport(ownRegistryTransport{transport}), remote.WithPlatform(v1.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}))
	if e != nil {
		return fail()
	}
	phase = "config"
	config, e := image.ConfigFile()
	if e != nil {
		return fail()
	}
	raw, e := json.Marshal(config.Config)
	if e != nil {
		return fail()
	}
	if e = b.Application.AdmitImageConfiguration(raw); e != nil {
		return b, e
	}
	id, e := image.ConfigName()
	if e != nil {
		return fail()
	}
	archive, e := os.CreateTemp("", "stackkit-own-image-*.tar")
	if e != nil {
		return fail()
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	tag, e := name.NewTag("stackkit.invalid/own-import:"+id.Hex, name.StrictValidation)
	if e != nil {
		return fail()
	}
	phase = "archive"
	if e = tarball.Write(tag, image, archive); e != nil {
		return fail()
	}
	if e = archive.Close(); e != nil {
		return fail()
	}
	command := exec.CommandContext(ctx, "docker", "--host", ownLocalDockerEndpoint(), "image", "load", "--input", archive.Name())
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	phase = "local archive load"
	if e = command.Run(); e != nil {
		return fail()
	}
	b.ImageID = id.String()
	if e = r.admitImageConfiguration(ctx, b); e != nil {
		return b, e
	}
	return b, nil
}
func ownLocalDockerEndpoint() string {
	if runtime.GOOS == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return "unix:///var/run/docker.sock"
}
