//go:build !windows

package generationartifact

import "os"

func atomicReplace0600(source, target string) error {
	return os.Rename(source, target)
}
