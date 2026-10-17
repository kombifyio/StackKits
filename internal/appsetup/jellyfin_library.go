package appsetup

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// ErrJellyfinLibraryCreateUnresolved does not authorize another create. A
// timed-out request can still commit after an absent observation on retry.
var ErrJellyfinLibraryCreateUnresolved = errors.New("Jellyfin library creation is unconfirmed; retry setup to observe it without sending another create")

// EnsureImportedLibrary registers only the native import directory. Existing
// libraries are never edited, and caller-owned cleanup revokes this session.
// beginCreate must durably mark dispatch before returning true. False admits
// only observation until a matching library is positively read back.
func (r *JellyfinOwnerResult) EnsureImportedLibrary(ctx context.Context, client *http.Client, baseURL string, beginCreate func() (bool, error)) error {
	if r == nil || !r.UserIsAdmin || !r.StartupWizardCompleted || r.accessToken == "" {
		return errors.New("Media library setup requires the verified Jellyfin administrator")
	}
	if beginCreate == nil {
		return errors.New("Media library setup requires durable create-dispatch custody")
	}
	type library struct {
		Name      string   `json:"Name"`
		Locations []string `json:"Locations"`
	}
	var libraries []library
	read := func() error {
		return jellyfinJSONRequest(ctx, client, baseURL, http.MethodGet, "/Library/VirtualFolders", r.accessToken, nil, &libraries)
	}
	if err := read(); err != nil {
		return err
	}
	find := func() (bool, error) {
		for _, item := range libraries {
			for _, location := range item.Locations {
				if location == "/media/Imports" {
					if len(item.Locations) != 1 {
						return false, errors.New("the existing import library includes other owner-managed sources")
					}
					return true, nil
				}
			}
			if item.Name == "Imported media" {
				return false, errors.New("the existing Imported media library points to another source")
			}
		}
		return false, nil
	}
	found, err := find()
	if err != nil {
		return err
	}
	if !found {
		allowed, err := beginCreate()
		if err != nil {
			return err
		}
		if !allowed {
			return ErrJellyfinLibraryCreateUnresolved
		}
		payload := map[string]any{"LibraryOptions": map[string]any{"PathInfos": []map[string]string{{"Path": "/media/Imports"}}, "EnableRealtimeMonitor": true, "SaveLocalMetadata": false}}
		if _, err := jellyfinJSONRequestQuery(ctx, client, baseURL, http.MethodPost, "/Library/VirtualFolders", r.accessToken, url.Values{"name": {"Imported media"}, "refreshLibrary": {"false"}}, payload, nil); err != nil {
			return err
		}
		if err := read(); err != nil {
			return err
		}
		found, err = find()
		if err != nil {
			return err
		}
		if !found {
			return ErrJellyfinLibraryCreateUnresolved
		}
	}
	return jellyfinJSONRequest(ctx, client, baseURL, http.MethodPost, "/Library/Refresh", r.accessToken, nil, nil)
}
