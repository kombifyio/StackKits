//go:build !windows

package backupcustody

func restrictPathToCurrentUser(string, bool) error {
	return nil
}

func requirePrivatePath(string, bool) error {
	return nil
}
