package upgradelifecycle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func copyWorkspace(source, target string, maxFiles int, maxBytes int64) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace must be a real directory")
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return err
	}
	count := 0
	var total int64
	return filepath.Walk(source, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		portable := filepath.ToSlash(relative)
		if portable == ".git" || strings.HasPrefix(portable, ".git/") ||
			portable == ".stackkit/releases" || strings.HasPrefix(portable, ".stackkit/releases/") ||
			portable == ".stackkit/logs" || strings.HasPrefix(portable, ".stackkit/logs/") ||
			portable == ".stackkit/evidence" || strings.HasPrefix(portable, ".stackkit/evidence/") ||
			portable == ".stackkit/custody" || strings.HasPrefix(portable, ".stackkit/custody/") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace path %q is a symlink", portable)
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace path %q is not a regular file", portable)
		}
		count++
		total += info.Size()
		if count > maxFiles || info.Size() < 0 || total > maxBytes {
			return fmt.Errorf("workspace exceeds inspection limits (%d files, %d bytes)", maxFiles, maxBytes)
		}
		input, err := os.Open(current)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		written, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != info.Size() {
			return fmt.Errorf("workspace file %q changed while copying", portable)
		}
		return nil
	})
}
