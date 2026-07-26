package upgradelifecycle

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractArchive(archivePath, name, root string, maxFiles int, maxBytes int64) error {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer reader.Close()
		var total int64
		seen := map[string]struct{}{}
		for _, entry := range reader.File {
			if err := extractEntry(root, entry.Name, entry.FileInfo().Mode(), entry.UncompressedSize64, &total, maxFiles, maxBytes, seen, func() (io.ReadCloser, error) {
				return entry.Open()
			}); err != nil {
				return err
			}
		}
		return nil
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	var stream io.Reader = file
	if strings.HasSuffix(strings.ToLower(name), ".gz") || strings.HasSuffix(strings.ToLower(name), ".tgz") {
		gz, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gz.Close()
		stream = gz
	}
	reader := tar.NewReader(stream)
	var total int64
	count := 0
	seen := map[string]struct{}{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		count++
		if count > maxFiles {
			return fmt.Errorf("archive exceeds %d entries", maxFiles)
		}
		entryName := header.Name
		if header.Typeflag == tar.TypeDir {
			entryName = strings.TrimRight(entryName, "/")
		}
		relative, err := safeRelative(entryName)
		if err != nil {
			return fmt.Errorf("unsafe archive path %q: %w", header.Name, err)
		}
		key := strings.ToLower(relative)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate archive path %q", header.Name)
		}
		seen[key] = struct{}{}
		target := filepath.Join(root, filepath.FromSlash(relative))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += header.Size
			if header.Size < 0 || total > maxBytes {
				return fmt.Errorf("archive exceeds %d extracted bytes", maxBytes)
			}
			if err := writeExtracted(target, os.FileMode(header.Mode), io.LimitReader(reader, header.Size), header.Size); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q has forbidden type %d", header.Name, header.Typeflag)
		}
	}
}

func extractEntry(root, name string, mode os.FileMode, size uint64, total *int64, maxFiles int, maxBytes int64, seen map[string]struct{}, open func() (io.ReadCloser, error)) error {
	if len(seen)+1 > maxFiles {
		return fmt.Errorf("archive exceeds %d entries", maxFiles)
	}
	entryName := name
	if mode.IsDir() {
		entryName = strings.TrimRight(entryName, "/")
	}
	relative, err := safeRelative(entryName)
	if err != nil {
		return fmt.Errorf("unsafe archive path %q: %w", name, err)
	}
	key := strings.ToLower(relative)
	if _, exists := seen[key]; exists {
		return fmt.Errorf("duplicate archive path %q", name)
	}
	seen[key] = struct{}{}
	target := filepath.Join(root, filepath.FromSlash(relative))
	if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
		return fmt.Errorf("archive entry %q is not a regular file or directory", name)
	}
	if mode.IsDir() {
		return os.MkdirAll(target, 0o700)
	}
	if size > uint64(maxBytes) || *total > maxBytes-int64(size) {
		return fmt.Errorf("archive exceeds %d extracted bytes", maxBytes)
	}
	*total += int64(size)
	reader, err := open()
	if err != nil {
		return err
	}
	defer reader.Close()
	return writeExtracted(target, mode, io.LimitReader(reader, int64(size)), int64(size))
}

func writeExtracted(target string, mode os.FileMode, reader io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	permissions := mode.Perm()
	if permissions == 0 {
		permissions = 0o600
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != size {
		return fmt.Errorf("archive entry %s truncated: got %d of %d bytes", filepath.Base(target), written, size)
	}
	return nil
}
