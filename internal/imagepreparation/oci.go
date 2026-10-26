package imagepreparation

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	oci "github.com/opencontainers/image-spec/specs-go/v1"
)

// inspectOCIArchive authenticates the content graph BEFORE Docker reads an
// archive. The unsigned bundle's file checksum is a transport check only;
// authority is the exact catalog digest and every reachable OCI descriptor.
// Classic Docker exports without their original OCI graph are not admitted.
func InspectOCIArchive(name string, image Image, platform Platform) error {
	if err := SafePath(name); err != nil {
		return err
	}
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return inspectOCIArchive(file, image, platform)
}

func inspectOCIArchive(input io.Reader, image Image, platform Platform) error {
	reader := tar.NewReader(input)
	type content struct {
		size     int64
		metadata []byte
	}
	blobs := map[string]content{}
	seen := map[string]bool{}
	var index []byte
	var totalBytes, metadataBytes int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if name != path.Clean(name) || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || seen[name] {
			return errors.New("offline image archive has a repeated or unsafe path")
		}
		seen[name] = true
		if len(seen) > 20000 {
			return errors.New("offline image archive has too many entries")
		}
		if header.Typeflag == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" {
				return errors.New("offline image archive has an unexpected directory")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 16<<30 {
			return errors.New("offline image archive requires bounded regular files")
		}
		totalBytes += header.Size
		if totalBytes > 16<<30 {
			return errors.New("offline image archive exceeds its content budget")
		}
		if strings.HasPrefix(name, "blobs/sha256/") {
			digest := strings.TrimPrefix(name, "blobs/sha256/")
			if !isSHA256(digest) || header.Size == 0 {
				return errors.New("offline image blob has an invalid digest or size")
			}
			hash := sha256.New()
			var metadata []byte
			if header.Size <= 4<<20 {
				metadata, err = io.ReadAll(io.TeeReader(reader, hash))
			} else {
				_, err = io.Copy(hash, reader)
			}
			if err != nil {
				return err
			}
			if hex.EncodeToString(hash.Sum(nil)) != digest {
				return fmt.Errorf("offline image blob %s failed its content digest", digest)
			}
			if !json.Valid(metadata) || !bytes.HasPrefix(bytes.TrimSpace(metadata), []byte("{")) {
				metadata = nil
			}
			metadataBytes += int64(len(metadata))
			if metadataBytes > 16<<20 {
				return errors.New("offline image archive exceeds its descriptor metadata budget")
			}
			blobs["sha256:"+digest] = content{size: header.Size, metadata: metadata}
			continue
		}
		if header.Size > 4<<20 || (name != "index.json" && name != "oci-layout" && name != "manifest.json") {
			return errors.New("offline image archive has an unrecognized entry")
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if name == "index.json" {
			index = raw
		}
		if name == "oci-layout" {
			var layout oci.ImageLayout
			if json.Unmarshal(raw, &layout) != nil || layout.Version != "1.0.0" {
				return errors.New("offline image requires an OCI 1.0 layout")
			}
		}
	}
	var roots oci.Index
	if !seen["oci-layout"] || json.Unmarshal(index, &roots) != nil || roots.SchemaVersion != 2 || len(roots.Manifests) != 1 {
		return errors.New("offline image requires exactly one original OCI image root")
	}
	if roots.Manifests[0].Digest.String() != image.Digest {
		return fmt.Errorf("offline image root differs from catalog pin %s", image.Digest)
	}
	visited := map[string]bool{}
	matchedPlatform := false
	var visit func(oci.Descriptor, int) error
	visit = func(descriptor oci.Descriptor, depth int) error {
		if depth > 6 {
			return errors.New("offline image descriptor graph is too deep")
		}
		digest := descriptor.Digest.String()
		blob, ok := blobs[digest]
		if !ok || descriptor.Size != blob.size || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
			return errors.New("offline image descriptor is missing, external or has a different size")
		}
		if visited[digest] {
			return nil
		}
		visited[digest] = true
		switch descriptor.MediaType {
		case oci.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
			var document oci.Index
			if json.Unmarshal(blob.metadata, &document) != nil || document.SchemaVersion != 2 || len(document.Manifests) == 0 {
				return errors.New("offline image index is invalid")
			}
			// A platform-filtered export may retain the original index but
			// omit nonselected variants. Only the observed host is admitted.
			for _, child := range document.Manifests {
				if child.Platform != nil && child.Platform.OS == platform.OS && child.Platform.Architecture == platform.Arch {
					if err := visit(child, depth+1); err != nil {
						return err
					}
				}
			}
		case oci.MediaTypeImageManifest, "application/vnd.docker.distribution.manifest.v2+json":
			var document oci.Manifest
			if json.Unmarshal(blob.metadata, &document) != nil || document.SchemaVersion != 2 || len(document.Layers) == 0 {
				return errors.New("offline image manifest is invalid")
			}
			if err := visit(document.Config, depth+1); err != nil {
				return err
			}
			for _, layer := range document.Layers {
				if err := visit(layer, depth+1); err != nil {
					return err
				}
			}
		case oci.MediaTypeImageConfig, "application/vnd.docker.container.image.v1+json":
			var config oci.Image
			if json.Unmarshal(blob.metadata, &config) != nil || config.OS != platform.OS || config.Architecture != platform.Arch {
				return errors.New("offline image configuration has another platform")
			}
			matchedPlatform = true
		case oci.MediaTypeImageLayer, oci.MediaTypeImageLayerGzip, oci.MediaTypeImageLayerZstd, "application/vnd.docker.image.rootfs.diff.tar.gzip":
		default:
			return fmt.Errorf("offline image descriptor media type %q is not admitted", descriptor.MediaType)
		}
		return nil
	}
	if err := visit(roots.Manifests[0], 0); err != nil {
		return err
	}
	if !matchedPlatform || len(visited) != len(blobs) {
		return errors.New("offline image contains missing platform content or unreferenced blobs")
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
