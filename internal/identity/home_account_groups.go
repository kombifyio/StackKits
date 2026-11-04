package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// The household groups of every account with a device (homelab identity
// target §7: the route's groups are the ceiling). A device credential, a
// browser session and an app pass carry no groups; Pocket ID, where the
// household membership is projected, is their authority. The host-side signer
// reads them with the Pocket ID admin key every minute and writes this
// secret-free projection into the verification custody; the Access Gate
// knows an account's groups only while the projection is at most
// homeAccountGroupsMaxAge old, and otherwise refuses a grouped route away.
const (
	homeAccountGroupsPath     = ".stackkit/custody/home-verifier/account-groups.json"
	homeAccountGroupsSchema   = "stackkit.home-account-groups/v1"
	homeAccountGroupsRefresh  = time.Minute
	homeAccountGroupsMaxAge   = time.Hour
	homeAccountGroupsMaxBytes = 256 << 10
	homeAccountGroupsCacheTTL = 5 * time.Second
)

var homeGroupName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type homeAccountGroupsRecord struct {
	Schema      string              `json:"schema"`
	RefreshedAt time.Time           `json:"refreshedAt"`
	Accounts    map[string][]string `json:"accounts"`
}

// RefreshHomeAccountGroups reads the groups of every account that holds a
// device entry from the Authority's Pocket ID (host only: it needs the admin
// key) and writes the projection. A removed or disabled account has none.
func RefreshHomeAccountGroups(ctx context.Context, root string, now time.Time) error {
	key, err := localevidence.ReadBasementRuntimePocketIDAdminKey(root)
	if err != nil {
		return err
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return err
	}
	client := pocketid.NewClient(localowner.PocketIDLocalAPI(), key)
	record := homeAccountGroupsRecord{Schema: homeAccountGroupsSchema, RefreshedAt: now.UTC().Truncate(time.Second), Accounts: map[string][]string{}}
	for _, device := range registry.Devices {
		account := device.AccountSubject
		if device.Revoked || account == "" {
			continue
		}
		if _, done := record.Accounts[account]; done {
			continue
		}
		groups := []string{}
		user, userErr := client.GetUser(ctx, account)
		switch {
		case errors.Is(userErr, pocketid.ErrNotFound):
		case userErr != nil:
			return userErr
		case user != nil && user.ID == account && !user.Disabled:
			for _, group := range user.UserGroups {
				if homeGroupName.MatchString(group.Name) && !slices.Contains(groups, group.Name) {
					groups = append(groups, group.Name)
				}
			}
		}
		slices.Sort(groups)
		record.Accounts[account] = groups
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeAccountGroupsPath, raw, false)
}

// keepHomeAccountGroupsCurrent refreshes the projection every minute.
func keepHomeAccountGroupsCurrent(ctx context.Context, root string) {
	for {
		refresh, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := RefreshHomeAccountGroups(refresh, root, time.Now()); err != nil {
			slog.Warn("household groups were not refreshed; grouped routes away fail closed once the projection is an hour old", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(homeAccountGroupsRefresh):
		}
	}
}

type homeAccountGroupsCache struct {
	mu     sync.Mutex
	read   time.Time
	record *homeAccountGroupsRecord
}

var homeAccountGroupsCaches sync.Map

// HomeAccountGroups returns the household groups of account and whether they
// are known: the projection is readable, at most homeAccountGroupsMaxAge old,
// and lists the account.
func HomeAccountGroups(root, account string, now time.Time) ([]string, bool) {
	absolute, err := filepath.Abs(root)
	if err != nil || account == "" {
		return nil, false
	}
	value, _ := homeAccountGroupsCaches.LoadOrStore(absolute, &homeAccountGroupsCache{})
	cache := value.(*homeAccountGroupsCache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.record == nil || now.Sub(cache.read) >= homeAccountGroupsCacheTTL || now.Before(cache.read) {
		cache.read, cache.record = now, nil
		if record, loadErr := loadHomeAccountGroups(absolute); loadErr == nil {
			cache.record = &record
		}
	}
	if cache.record == nil || now.Sub(cache.record.RefreshedAt) > homeAccountGroupsMaxAge || cache.record.RefreshedAt.After(now.Add(HomeClockLeeway)) {
		return nil, false
	}
	groups, ok := cache.record.Accounts[account]
	return slices.Clone(groups), ok
}

func loadHomeAccountGroups(root string) (homeAccountGroupsRecord, error) {
	var record homeAccountGroupsRecord
	raw, err := readHomePrivate(root, homeAccountGroupsPath, homeAccountGroupsMaxBytes)
	if err != nil {
		return record, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.Schema != homeAccountGroupsSchema || record.Accounts == nil {
		return homeAccountGroupsRecord{}, errors.New("identity: unreadable household groups")
	}
	return record, nil
}
