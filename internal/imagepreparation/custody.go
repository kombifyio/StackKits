package imagepreparation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

type stagedBundleImage struct {
	image BundleImage
	file  *os.File
}

type bundleImageCustody struct{ images []stagedBundleImage }

func (custody *bundleImageCustody) close() error {
	var err error
	for _, staged := range custody.images {
		err = errors.Join(err, staged.file.Close())
	}
	custody.images = nil
	return err
}

// All archives enter private, unlinked custody and authenticate there before
// the first Docker call. No source or custody pathname survives as load input.
func stageBundleImages(ctx context.Context, admission *FreshBundleAdmission) (result *bundleImageCustody, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, errors.New("offline image custody requires the supported Linux host")
	}
	input, err := os.OpenRoot(admission.ref.Path)
	if err != nil {
		return nil, err
	}
	// Ignore ambient TMPDIR, which may name the operator's transport bundle.
	// OpenRoot pins the directory across ancestor replacement during creation
	// and unlink. Only the open, mode-0600 inode holds copied archive bytes.
	temporary, err := os.OpenRoot("/tmp")
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	custody := &bundleImageCustody{}
	defer func() {
		err = errors.Join(err, input.Close(), temporary.Close())
		if err != nil {
			err = errors.Join(err, custody.close())
			result = nil
		}
	}()
	for _, image := range admission.manifest.Images {
		expected, ok := admission.manifest.Files[image.Path]
		if !ok || !isSHA256(expected.SHA256) || expected.Size <= 0 || expected.Size > 16<<30 {
			return nil, errors.New("offline image has no bounded expected byte closure")
		}
		file, err := stageBundleImage(ctx, input, temporary, admission.ref.Path, image, expected, admission.manifest.Neutral.Platform)
		if err != nil {
			return nil, fmt.Errorf("stage offline image %s: %w", image.Image.Component, err)
		}
		custody.images = append(custody.images, stagedBundleImage{image: image, file: file})
	}
	return custody, nil
}

func stageBundleImage(ctx context.Context, input, temporary *os.Root, root string, image BundleImage, expected BundleFile, platform Platform) (result *os.File, err error) {
	if err := SafePath(filepath.Join(root, filepath.FromSlash(image.Path))); err != nil {
		return nil, err
	}
	before, err := input.Lstat(filepath.FromSlash(image.Path))
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() != expected.Size {
		return nil, errors.New("offline image input must be a regular file of its expected size")
	}
	// A concurrent replacement with a FIFO must not block opening the input.
	source, err := input.OpenFile(filepath.FromSlash(image.Path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	var staged *os.File
	defer func() {
		err = errors.Join(err, source.Close())
		if err != nil && staged != nil {
			err = errors.Join(err, staged.Close())
			result = nil
		}
	}()
	opened, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != expected.Size {
		return nil, errors.New("offline image input changed while opening")
	}
	name := "stackkit-offline-image-" + rand.Text()
	staged, err = temporary.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	// Unlink before copying any bytes. Input writers cannot reach this inode
	// through a replaced archive, custody filename or custody ancestor.
	if err := temporary.Remove(name); err != nil {
		return nil, fmt.Errorf("unlink empty offline image custody file %s: %w", name, err)
	}
	copied, copyErr := io.Copy(staged, io.LimitReader(bundleContextReader{ctx: ctx, input: source}, expected.Size+1))
	after, statErr := source.Stat()
	if err := errors.Join(copyErr, statErr); err != nil {
		return nil, err
	}
	if copied != expected.Size || after.Size() != expected.Size || !after.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("offline image input changed during staging")
	}
	// Copying is not authentication: hash the private result and run the same
	// catalog-owned OCI graph verifier against that exact open file.
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, bundleContextReader{ctx: ctx, input: staged})
	if err != nil {
		return nil, err
	}
	if count != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return nil, errors.New("staged offline image differs from its expected byte closure")
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err := inspectOCIArchive(bundleContextReader{ctx: ctx, input: staged}, image.Image, platform); err != nil {
		return nil, err
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return staged, nil
}

type bundleContextReader struct {
	ctx   context.Context
	input io.Reader
}

func (reader bundleContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.input.Read(buffer)
}
