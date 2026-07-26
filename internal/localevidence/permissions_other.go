//go:build !windows

package localevidence

func restrictFileToCurrentUser(string) error {
	return nil
}
