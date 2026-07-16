//go:build !windows

package commands

import "os"

func atomicReplaceMigrationFile(source, target string) error {
	return os.Rename(source, target)
}
