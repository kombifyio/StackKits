//go:build windows

package localevidence

import "os"

// Windows is not a Core target. Docker Desktop maps bind mounts as root, so
// development renders run the server as root.
func stackKitServerCredentialOwner(directory string) (string, bool) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return "0:0", true
}
