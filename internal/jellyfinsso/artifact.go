// Package jellyfinsso carries the one governed Jellyfin SSO plugin. It never
// downloads or extracts files onto the host filesystem.
package jellyfinsso

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
)

const (
	Version         = "5.1.1"
	GUID            = "505ce9d1-d916-42fa-86ca-673ef241d7df"
	SHA256          = "sha256:db126b4763bda3a3754fa2103232fc03ef1bf929cfeda35f2e9af39c875233b1"
	SourceURL       = "https://github.com/K0lin/jellyfin-plugin-sso/releases/download/v5.1.1/sso-authentication_5.1.1.zip"
	TargetDirectory = "/config/plugins/stackkit-sso_" + Version
)

//go:embed sso-authentication_5.1.1.zip
var archive []byte

// Files returns only the governed assemblies. Jellyfin owns writable metadata
// and account-link configuration beneath its persistent config volume.
func Files(version, digest, source string) (map[string][]byte, error) {
	sum := sha256.Sum256(archive)
	if version != Version || digest != SHA256 || source != SourceURL || "sha256:"+hex.EncodeToString(sum[:]) != SHA256 {
		return nil, errors.New("Jellyfin SSO artifact does not match its governed pin")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, errors.New("invalid bundled Jellyfin SSO artifact")
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		switch file.Name {
		case "meta.json":
			continue // Jellyfin creates its own non-updating metadata.
		case "SSO-Auth.dll", "Duende.IdentityModel.dll", "Duende.IdentityModel.OidcClient.dll":
		default:
			return nil, errors.New("unexpected bundled Jellyfin SSO artifact member")
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
		_ = rc.Close()
		if err != nil || len(data) == 0 || len(data) >= 4<<20 {
			return nil, errors.New("invalid bundled Jellyfin SSO assembly")
		}
		files[file.Name] = data
	}
	return files, nil
}
