// Package hassoidc packages the reviewed Home Assistant OIDC extension and
// private Python dependencies without an install-time network fetch.
package hassoidc

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
)

const (
	Version         = "1.2.1"
	PatchSHA256     = "sha256:052c4e0b85b962a5fad74e395f250f4d5cf9eff63fa5be68fc1c4494535d1a8a"
	PatchVersion    = "stackkit-owner-binding-v1"
	SHA256          = "sha256:e5badaaacaa63cfd6fe733924a05e76d75058836190398598fb24de57cd47ccd"
	SourceURL       = "https://github.com/christiaangoossens/hass-oidc-auth/releases/download/v1.2.1/hass-oidc-auth.zip"
	AiofilesSHA256  = "sha256:abe311e527c862958650f9438e859c1fa7568a141b22abcd015e120e86a85695"
	JoseRFCSHA256   = "sha256:17e5d7a5a35e65442b05efc435a3d5d46696ffa2c8a2ed0eea6f63fc268e3224"
	TargetDirectory = "/config/custom_components/auth_oidc"
)

//go:embed hass-oidc-auth-1.2.1.zip
var archive []byte

//go:embed aiofiles-25.1.0.whl
var aiofiles []byte

//go:embed joserfc-1.7.0.whl
var joserfc []byte

//go:embed LICENSE.upstream
var upstreamLicense []byte

//go:embed stackkit_binding.py
var binding []byte

// Files verifies upstream artifacts, applies the explicit reviewed source
// patch and returns only container-visible bytes. It does not write to disk.
func Files() (map[string][]byte, error) {
	patchSum := sha256.Sum256(binding)
	if "sha256:"+hex.EncodeToString(patchSum[:]) != PatchSHA256 {
		return nil, errors.New("Home Assistant OIDC binding patch differs from its pin")
	}
	files, err := unpack(archive, SHA256)
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return nil, err
	}
	// All requirements are provided by the immutable image or private vendored
	// wheels. Home Assistant must never invoke its online requirement installer.
	manifest["requirements"] = []string{}
	files["manifest.json"], err = json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	replacements := map[string][][2]string{
		"views/loader.py":                 {{"from aiofiles.os import", "from .._vendor.aiofiles.os import"}, {"from aiofiles import", "from .._vendor.aiofiles import"}},
		"endpoints/injected_auth_page.py": {{"from aiofiles import", "from .._vendor.aiofiles import"}},
		"tools/oidc_client.py":            {{"from joserfc import", "from .._vendor.joserfc import"}},
		"__init__.py":                     {{"from .provider import OpenIDAuthProvider", "from .provider import OpenIDAuthProvider\nfrom .stackkit_binding import register_binding"}, {"    # Set the correct scopes", "    await register_binding(hass, provider, my_config)\n\n    # Set the correct scopes"}},
		"provider.py":                     {{"from .tools.types import UserDetails", "from .tools.types import UserDetails\nfrom .stackkit_binding import verify_login_binding"}, {"                return credential\n\n        # If no credential was found", "                await verify_login_binding(self, credential, sub, meta)\n                return credential\n\n        await verify_login_binding(self, None, sub, meta)\n\n        # If no credential was found"}},
	}
	for name, edits := range replacements {
		body := string(files[name])
		for _, edit := range edits {
			if strings.Count(body, edit[0]) != 1 {
				return nil, errors.New("Home Assistant OIDC patch no longer matches upstream")
			}
			body = strings.Replace(body, edit[0], edit[1], 1)
		}
		files[name] = []byte(body)
	}
	files["stackkit_binding.py"] = append([]byte(nil), binding...)
	files["LICENSE"] = append([]byte(nil), upstreamLicense...)
	files["_vendor/__init__.py"] = []byte("# Private, digest-pinned upstream dependencies.\n")
	for _, wheel := range []struct {
		data         []byte
		digest, name string
	}{{aiofiles, AiofilesSHA256, "aiofiles"}, {joserfc, JoseRFCSHA256, "joserfc"}} {
		contents, err := unpack(wheel.data, wheel.digest)
		if err != nil {
			return nil, err
		}
		for name, data := range contents {
			if strings.HasPrefix(name, wheel.name+"/") {
				files["_vendor/"+name] = data
			} else if strings.Contains(name, ".dist-info/licenses/") {
				files["_vendor/"+wheel.name+"/"+path.Base(name)] = data
			}
		}
	}
	return files, nil
}

func unpack(data []byte, digest string) (map[string][]byte, error) {
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != digest {
		return nil, errors.New("Home Assistant OIDC artifact differs from its pin")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if path.Clean(file.Name) != file.Name || strings.HasPrefix(file.Name, "/") || strings.HasPrefix(file.Name, "../") || strings.Contains(file.Name, "\\") || !file.Mode().IsRegular() {
			return nil, errors.New("invalid OIDC artifact member")
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, 2<<20))
		_ = rc.Close()
		if err != nil || len(body) >= 2<<20 {
			return nil, errors.New("invalid OIDC artifact size")
		}
		files[file.Name] = body
	}
	return files, nil
}
