package identity

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// App passes (homelab identity target §6.3, §12): where the Owner allowed app
// access away for a service (appAccessAway on its access policy), the Hub
// issues a device's app (Immich, a WebDAV client ...) a pass of at most 30
// days for that one host. The app sends it in the HomeAppPassHeader, which the
// router strips before the backend; the Access Gate admits it on that host's
// app routes only while the pass and its device are active, so revoking the
// device ends its passes. The Authority keeps only a hash of each pass.
const (
	HomeAppPassHeader   = "Stackkit-App-Pass"
	HomeAppPassLifetime = 30 * 24 * time.Hour
	// HomeAppPassesPath lists (GET) and issues (POST) the caller's passes;
	// POST HomeAppPassesPath/{id}/revoke revokes one.
	HomeAppPassesPath = "/api/v1/identity/home/app-passes"

	homeAppPassPrefix          = "skap1_"
	homeAppPassesFile          = ".stackkit/custody/home-verifier/app-passes.json"
	homeAppPassesSchema        = "stackkit.home-app-passes/v1"
	homeAppPassesMaxBytes      = 256 << 10
	homeAppPassesPerDevice     = 16
	homeAppPassesCapacity      = 512
	homeAppPassFreshPasskey    = 10 * time.Minute
	homeAppPassesCacheTTL      = 2 * time.Second
	homeAppPassRetentionRevoke = 7 * 24 * time.Hour
)

var ErrHomeAppPassFreshPasskey = errors.New("identity: issuing an app pass needs a passkey sign-in on this device within the last 10 minutes")

// HomeAppPass is one pass, without its secret.
type HomeAppPass struct {
	ID        string     `json:"id"`
	Host      string     `json:"host"`
	Name      string     `json:"name"`
	Account   string     `json:"account"`
	Device    string     `json:"device"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

type homeAppPassRecord struct {
	HomeAppPass
	Hash string `json:"hash"`
}

type homeAppPasses struct {
	Schema string              `json:"schema"`
	Passes []homeAppPassRecord `json:"passes"`
}

// IssueHomeAppPass issues a pass for host to the calling device, whose last
// passkey user verification must be at most 10 minutes old. host must be one
// the Owner opened to app passes. The secret is returned once.
func IssueHomeAppPass(root string, caller HomeAuthentication, host, name string, appHosts []string, now time.Time) (string, HomeAppPass, error) {
	now = now.UTC()
	if !slices.Contains(appHosts, host) || !ValidHomeDeviceName(name) {
		return "", HomeAppPass{}, ErrHomeNoticeNotFound
	}
	if now.Sub(caller.UserVerifiedAt) > homeAppPassFreshPasskey {
		return "", HomeAppPass{}, ErrHomeAppPassFreshPasskey
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", HomeAppPass{}, err
	}
	secret := homeAppPassPrefix + base64.RawURLEncoding.EncodeToString(raw)
	id, err := homeRandomID()
	if err != nil {
		return "", HomeAppPass{}, err
	}
	pass := HomeAppPass{ID: id, Host: host, Name: name, Account: caller.Subject, Device: caller.Device,
		CreatedAt: now.Truncate(time.Second), ExpiresAt: now.Truncate(time.Second).Add(HomeAppPassLifetime)}
	err = updateHomeAppPasses(root, func(passes *homeAppPasses) error {
		kept, perDevice := passes.Passes[:0], 0
		for _, existing := range passes.Passes {
			if existing.RevokedAt != nil && now.Sub(*existing.RevokedAt) > homeAppPassRetentionRevoke || now.Sub(existing.ExpiresAt) > homeAppPassRetentionRevoke {
				continue
			}
			if existing.Device == caller.Device && existing.RevokedAt == nil && now.Before(existing.ExpiresAt) {
				perDevice++
			}
			kept = append(kept, existing)
		}
		if perDevice >= homeAppPassesPerDevice || len(kept) >= homeAppPassesCapacity {
			return ErrHomeEnrollmentRateLimited
		}
		passes.Passes = append(kept, homeAppPassRecord{HomeAppPass: pass, Hash: homeTokenHash(secret)})
		return nil
	})
	if err != nil {
		return "", HomeAppPass{}, err
	}
	return secret, pass, nil
}

// ListHomeAppPasses returns the actor's passes (the Owner's devices: all).
func ListHomeAppPasses(root string, actor HomeActor) ([]HomeAppPass, error) {
	passes, err := loadHomeAppPasses(root)
	if err != nil {
		return nil, err
	}
	result := []HomeAppPass{}
	for _, record := range passes.Passes {
		if actor.Owner || record.Account == actor.Account {
			result = append(result, record.HomeAppPass)
		}
	}
	return result, nil
}

// RevokeHomeAppPass revokes one pass of the actor's account (Owner: any).
func RevokeHomeAppPass(root, id string, actor HomeActor, now time.Time) error {
	return updateHomeAppPasses(root, func(passes *homeAppPasses) error {
		for index := range passes.Passes {
			record := &passes.Passes[index]
			if record.ID != id || !actor.Owner && record.Account != actor.Account {
				continue
			}
			if record.RevokedAt == nil {
				at := now.UTC()
				record.RevokedAt = &at
			}
			return nil
		}
		return ErrHomeNoticeNotFound
	})
}

// revokeHomeAppPassesOfDevices ends every pass of the given devices; the
// device check at every request has already ended them.
func revokeHomeAppPassesOfDevices(root string, devices []string, now time.Time) (int, error) {
	revoked := 0
	err := updateHomeAppPasses(root, func(passes *homeAppPasses) error {
		for index := range passes.Passes {
			record := &passes.Passes[index]
			if record.RevokedAt == nil && slices.Contains(devices, record.Device) {
				at := now.UTC()
				record.RevokedAt, revoked = &at, revoked+1
			}
		}
		return nil
	})
	return revoked, err
}

// VerifyHomeAppPass admits a pass only for the host it was issued for,
// before its end, unrevoked, and while its device is an active registry entry
// of its account.
func (v *HomeVerifier) VerifyHomeAppPass(secret, host string) (HomeAppPass, error) {
	if !strings.HasPrefix(secret, homeAppPassPrefix) || len(secret) > 128 {
		return HomeAppPass{}, ErrHomeAccessDenied
	}
	now := v.now().UTC()
	passes, err := cachedHomeAppPasses(v.root, now)
	if err != nil {
		return HomeAppPass{}, ErrHomeAccessDenied
	}
	hash := homeTokenHash(secret)
	for _, record := range passes.Passes {
		if subtle.ConstantTimeCompare([]byte(record.Hash), []byte(hash)) != 1 {
			continue
		}
		if record.Host != host || record.RevokedAt != nil || !now.Before(record.ExpiresAt) || !v.activeDevice(record.Device, record.Account, now) {
			return HomeAppPass{}, ErrHomeAccessDenied
		}
		return record.HomeAppPass, nil
	}
	return HomeAppPass{}, ErrHomeAccessDenied
}

// registerHomeAppPassRoutes mounts the device-authenticated app pass routes.
func registerHomeAppPassRoutes(mux *http.ServeMux, root string, appHosts []string) {
	mux.Handle("GET "+HomeAppPassesPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, _ *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		passes, err := ListHomeAppPasses(root, actor)
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"passes": passes, "hosts": appHosts})
	}))
	mux.Handle("POST "+HomeAppPassesPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, _ *HomeVerifier, caller HomeAuthentication, _ HomeActor) {
		var body struct {
			Host string `json:"host"`
			Name string `json:"name"`
		}
		if !decodeHomeJSON(r.Body, homeIdentityMaxBodyBytes, &body, false) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		secret, pass, err := IssueHomeAppPass(root, caller, body.Host, body.Name, appHosts, time.Now())
		switch {
		case errors.Is(err, ErrHomeAppPassFreshPasskey):
			denyHomeIdentity(w, http.StatusUnauthorized, "user_verification_required", false)
		case errors.Is(err, ErrHomeNoticeNotFound):
			denyHomeIdentity(w, http.StatusForbidden, "app_access_not_allowed", false)
		case errors.Is(err, ErrHomeEnrollmentRateLimited):
			denyHomeIdentity(w, http.StatusTooManyRequests, "rate_limited", true)
		case err != nil:
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
		default:
			writeHomeIdentityJSON(w, http.StatusCreated, map[string]any{"pass": pass, "secret": secret, "header": HomeAppPassHeader})
		}
	}))
	mux.Handle("POST "+HomeAppPassesPath+"/{id}/revoke", homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		if err := RevokeHomeAppPass(root, r.PathValue("id"), actor, time.Now()); err != nil {
			denyHomeIdentity(w, http.StatusNotFound, "not_found", false)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"revoked": true})
	}))
}

func updateHomeAppPasses(root string, mutate func(*homeAppPasses) error) error {
	release, err := localevidence.AcquireCustodyLock(root, homeAppPassesFile, 101)
	if err != nil {
		return err
	}
	defer release()
	passes, err := loadHomeAppPasses(root)
	if err != nil {
		return err
	}
	if err := mutate(&passes); err != nil {
		return err
	}
	raw, err := json.Marshal(passes)
	if err != nil {
		return err
	}
	if len(raw) > homeAppPassesMaxBytes {
		return ErrHomeRegistryFull
	}
	if err := writeHomePrivate(root, homeAppPassesFile, raw, false); err != nil {
		return err
	}
	invalidateHomeAppPasses(root)
	return nil
}

func loadHomeAppPasses(root string) (homeAppPasses, error) {
	passes := homeAppPasses{Schema: homeAppPassesSchema, Passes: []homeAppPassRecord{}}
	raw, err := readHomePrivate(root, homeAppPassesFile, homeAppPassesMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return passes, nil
	}
	if err != nil {
		return passes, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&passes) != nil || decoder.Decode(new(any)) != io.EOF || passes.Schema != homeAppPassesSchema {
		return passes, errors.New("identity: unreadable app passes")
	}
	return passes, nil
}

type homeAppPassesCache struct {
	mu     sync.Mutex
	read   time.Time
	passes *homeAppPasses
}

var homeAppPassesCaches sync.Map

func cachedHomeAppPasses(root string, now time.Time) (homeAppPasses, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return homeAppPasses{}, err
	}
	value, _ := homeAppPassesCaches.LoadOrStore(absolute, &homeAppPassesCache{})
	cache := value.(*homeAppPassesCache)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.passes == nil || now.Sub(cache.read) >= homeAppPassesCacheTTL || now.Before(cache.read) {
		passes, loadErr := loadHomeAppPasses(absolute)
		if loadErr != nil {
			cache.passes = nil
			return homeAppPasses{}, loadErr
		}
		cache.read, cache.passes = now, &passes
	}
	return *cache.passes, nil
}

func invalidateHomeAppPasses(root string) {
	if absolute, err := filepath.Abs(root); err == nil {
		if value, ok := homeAppPassesCaches.Load(absolute); ok {
			cache := value.(*homeAppPassesCache)
			cache.mu.Lock()
			cache.passes = nil
			cache.mu.Unlock()
		}
	}
}
