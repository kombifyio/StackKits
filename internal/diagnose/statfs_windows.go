//go:build windows

package diagnose

func statDisk(_ string) (freeBytes, totalBytes uint64) {
	return 0, 0
}
