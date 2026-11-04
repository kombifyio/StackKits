package identity

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Web Push delivery of the notice records (homelab identity target §6.2): the
// Hub subscribes its own enrolled device through its push service, and the
// Authority sends each new enrollment or recovery notice to the subscriptions
// of exactly the devices the record names, encrypted to the subscription
// (RFC 8291) and signed with the Authority's VAPID key (RFC 8292). A payload
// carries the notice kind, its id and the device name, never a credential.
const (
	HomePushSubscriptionPath = "/api/v1/identity/home/push/subscription"

	homePushVAPIDPath         = ".stackkit/custody/home-verifier/web-push-vapid.json"
	homePushSubscriptionsPath = ".stackkit/custody/home-verifier/push-subscriptions.json"
	homePushDeliveredPath     = ".stackkit/custody/home-verifier/push-delivered.json"
	homePushVAPIDSchema       = "stackkit.home-web-push-vapid/v1"
	homePushSubscriptionsSch  = "stackkit.home-push-subscriptions/v1"
	homePushDeliveredSchema   = "stackkit.home-push-delivered/v1"
	homePushMaxBytes          = 256 << 10
	homePushMaxSubscriptions  = 512
	homePushInterval          = 15 * time.Second
	homePushNoticeWindow      = 24 * time.Hour
	homePushDeliveredKeep     = 8 * 24 * time.Hour
	homePushTTLSeconds        = 86400
	homePushRecordSize        = 4096
)

// homePushServiceHosts are the push services browsers subscribe through
// (Chrome: FCM and its jmt17 endpoint; Firefox: Mozilla autopush), and
// homePushServiceSuffixes the services that name a host per subscription
// (Apple, Windows). A subscription naming any other endpoint is refused, so
// the Authority never sends requests into the home network or to a host an
// enrolled device chooses.
var (
	homePushServiceHosts    = []string{"fcm.googleapis.com", "jmt17.google.com", "updates.push.services.mozilla.com"}
	homePushServiceSuffixes = []string{".push.apple.com", ".notify.windows.com"}
)

type homePushSubscription struct {
	DeviceID  string    `json:"deviceId"`
	Account   string    `json:"account"`
	Endpoint  string    `json:"endpoint"`
	P256DH    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	CreatedAt time.Time `json:"createdAt"`
}

type homePushSubscriptions struct {
	Schema        string                 `json:"schema"`
	Subscriptions []homePushSubscription `json:"subscriptions"`
}

type homePushDelivered struct {
	Schema    string               `json:"schema"`
	Delivered map[string]time.Time `json:"delivered"`
}

type homePushVAPIDFile struct {
	Schema    string    `json:"schema"`
	PKCS8     string    `json:"pkcs8"`
	CreatedAt time.Time `json:"createdAt"`
}

func registerHomePushRoutes(mux *http.ServeMux, root string) {
	mux.Handle("POST "+HomePushSubscriptionPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, _ *HomeVerifier, caller HomeAuthentication, _ HomeActor) {
		var body struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256DH string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		}
		if !decodeHomeJSON(r.Body, homeIdentityMaxBodyBytes, &body, false) || !validHomePushSubscription(body.Endpoint, body.Keys.P256DH, body.Keys.Auth) {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		if _, err := ensureHomePushVAPIDKey(root, time.Now()); err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		subscription := homePushSubscription{DeviceID: caller.Device, Account: caller.Subject, Endpoint: body.Endpoint, P256DH: body.Keys.P256DH, Auth: body.Keys.Auth, CreatedAt: time.Now().UTC()}
		err := updateHomePushSubscriptions(root, func(set *homePushSubscriptions) error {
			kept := set.Subscriptions[:0]
			for _, existing := range set.Subscriptions {
				if existing.DeviceID != caller.Device {
					kept = append(kept, existing)
				}
			}
			if len(kept) >= homePushMaxSubscriptions {
				return ErrHomeRegistryFull
			}
			set.Subscriptions = append(kept, subscription)
			return nil
		})
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		writeHomeIdentityJSON(w, http.StatusCreated, map[string]any{"subscribed": true})
	}))
	mux.Handle("DELETE "+HomePushSubscriptionPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, _ *http.Request, _ *HomeVerifier, caller HomeAuthentication, _ HomeActor) {
		err := updateHomePushSubscriptions(root, func(set *homePushSubscriptions) error {
			set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(s homePushSubscription) bool { return s.DeviceID == caller.Device })
			return nil
		})
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"subscribed": false})
	}))
}

func validHomePushSubscription(endpoint, p256dh, auth string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if !slices.Contains(homePushServiceHosts, host) && !slices.ContainsFunc(homePushServiceSuffixes, func(suffix string) bool { return strings.HasSuffix(host, suffix) }) {
		return false
	}
	key, keyErr := base64.RawURLEncoding.DecodeString(p256dh)
	secret, secretErr := base64.RawURLEncoding.DecodeString(auth)
	if keyErr != nil || secretErr != nil || len(secret) != 16 {
		return false
	}
	_, err = ecdh.P256().NewPublicKey(key)
	return err == nil
}

// deliverHomePushNotices sends new notices until ctx ends. Each notice is
// sent once to the subscriptions of its devices that are still active; a
// round that cannot read its inputs leaves the notices for the next round,
// and a notice older than a day is never sent.
func deliverHomePushNotices(ctx context.Context, root string) {
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(homePushInterval):
		}
		if err := deliverHomePushRound(ctx, root, client, time.Now().UTC()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("Web Push delivery round failed", "error", err)
		}
	}
}

type homePushMessage struct {
	ID         string `json:"noticeId"`
	Kind       string `json:"kind"`
	DeviceName string `json:"deviceName,omitempty"`
	devices    []string
}

func deliverHomePushRound(ctx context.Context, root string, client *http.Client, now time.Time) error {
	subscriptions, err := loadHomePushSubscriptions(root)
	if err != nil || len(subscriptions.Subscriptions) == 0 {
		return err
	}
	messages, err := pendingHomePushMessages(root, now)
	if err != nil || len(messages) == 0 {
		return err
	}
	key, err := ensureHomePushVAPIDKey(root, now)
	if err != nil {
		return err
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return err
	}
	gone := map[string]bool{}
	delivered := []string{}
	for _, message := range messages {
		payload, _ := json.Marshal(message)
		for _, subscription := range subscriptions.Subscriptions {
			if !slices.Contains(message.devices, subscription.DeviceID) || gone[subscription.DeviceID] {
				continue
			}
			// A device revoked since the notice was recorded gets nothing.
			if device, ok := findHomeDevice(registry.Devices, subscription.DeviceID); !ok || !device.Active(now) {
				continue
			}
			status, sendErr := sendHomeWebPush(ctx, root, client, key, subscription, payload, now)
			if status == http.StatusNotFound || status == http.StatusGone {
				gone[subscription.DeviceID] = true
			} else if sendErr != nil {
				slog.Debug("Web Push send failed", "device", subscription.DeviceID, "status", status)
			}
		}
		delivered = append(delivered, message.ID)
	}
	if len(gone) > 0 {
		_ = updateHomePushSubscriptions(root, func(set *homePushSubscriptions) error {
			set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(s homePushSubscription) bool { return gone[s.DeviceID] })
			return nil
		})
	}
	return updateHomePrivateJSON(root, homePushDeliveredPath, func(raw []byte) ([]byte, error) {
		state := homePushDelivered{Schema: homePushDeliveredSchema, Delivered: map[string]time.Time{}}
		if raw != nil && (json.Unmarshal(raw, &state) != nil || state.Schema != homePushDeliveredSchema) {
			state = homePushDelivered{Schema: homePushDeliveredSchema, Delivered: map[string]time.Time{}}
		}
		if state.Delivered == nil {
			state.Delivered = map[string]time.Time{}
		}
		for id, at := range state.Delivered {
			if now.Sub(at) > homePushDeliveredKeep {
				delete(state.Delivered, id)
			}
		}
		for _, id := range delivered {
			state.Delivered[id] = now
		}
		return json.Marshal(state)
	})
}

// pendingHomePushMessages collects the enrollment notices (to the person's
// other devices and, when the record says so, the Owner's devices) and the
// recovery notices (to the Owner devices they list) not delivered yet.
func pendingHomePushMessages(root string, now time.Time) ([]homePushMessage, error) {
	delivered := map[string]time.Time{}
	if raw, err := readHomePrivate(root, homePushDeliveredPath, homePushMaxBytes); err == nil {
		var state homePushDelivered
		if json.Unmarshal(raw, &state) == nil && state.Schema == homePushDeliveredSchema && state.Delivered != nil {
			delivered = state.Delivered
		}
	}
	registry, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return nil, err
	}
	owner := ""
	if trust, trustErr := loadCurrentHomeTrust(root, now); trustErr == nil {
		owner = trust.PocketID.Subject
	}
	messages := []homePushMessage{}
	for _, notice := range registry.Notices {
		if _, done := delivered[notice.ID]; done || now.Sub(notice.CreatedAt) > homePushNoticeWindow || notice.State != HomeNoticeOpen {
			continue
		}
		devices := append([]string{}, notice.NotifyDevices...)
		if notice.NotifyOwner && owner != "" {
			for _, device := range registry.Devices {
				if device.AccountSubject == owner && device.ID != notice.DeviceID && device.Active(now) && !slices.Contains(devices, device.ID) {
					devices = append(devices, device.ID)
				}
			}
		}
		messages = append(messages, homePushMessage{ID: notice.ID, Kind: notice.Kind, DeviceName: notice.DeviceName, devices: devices})
	}
	recovery, err := ListRecoveryNotices(root)
	if err == nil {
		for _, notice := range recovery {
			if _, done := delivered[notice.ID]; done || now.Sub(notice.CreatedAt) > homePushNoticeWindow {
				continue
			}
			messages = append(messages, homePushMessage{ID: notice.ID, Kind: "recovery-" + notice.Kind, devices: append([]string{}, notice.Devices...)})
		}
	}
	return messages, nil
}

// sendHomeWebPush encrypts payload to one subscription (aes128gcm, one record)
// and posts it with a VAPID authorization for the endpoint's origin.
func sendHomeWebPush(ctx context.Context, root string, client *http.Client, key *ecdsa.PrivateKey, subscription homePushSubscription, payload []byte, now time.Time) (int, error) {
	body, err := encryptHomeWebPush(subscription, payload)
	if err != nil {
		return 0, err
	}
	endpoint, err := url.Parse(subscription.Endpoint)
	if err != nil {
		return 0, err
	}
	subject, ok := homeHubOrigin(root)
	if !ok {
		return 0, errors.New("identity: no Hub origin for the VAPID subject")
	}
	authorization, err := homePushVAPIDAuthorization(key, "https://"+endpoint.Host, subject, now)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, subscription.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", fmt.Sprint(homePushTTLSeconds))
	request.Header.Set("Urgency", "high")
	request.Header.Set("Authorization", authorization)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, fmt.Errorf("identity: push service answered %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

// encryptHomeWebPush is the RFC 8291 message encryption: an ephemeral P-256
// key agreement with the subscription key, keyed by its auth secret, and one
// aes128gcm record (RFC 8188).
func encryptHomeWebPush(subscription homePushSubscription, payload []byte) ([]byte, error) {
	receiverRaw, err := base64.RawURLEncoding.DecodeString(subscription.P256DH)
	if err != nil {
		return nil, err
	}
	secret, err := base64.RawURLEncoding.DecodeString(subscription.Auth)
	if err != nil || len(secret) != 16 {
		return nil, errors.New("identity: invalid push auth secret")
	}
	receiver, err := ecdh.P256().NewPublicKey(receiverRaw)
	if err != nil {
		return nil, err
	}
	sender, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := sender.ECDH(receiver)
	if err != nil {
		return nil, err
	}
	senderRaw := sender.PublicKey().Bytes()
	keyInfo := append(append([]byte("WebPush: info\x00"), receiverRaw...), senderRaw...)
	ikm, err := hkdf.Key(sha256.New, shared, secret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	if len(payload)+1+16 > homePushRecordSize {
		return nil, errors.New("identity: push payload too large")
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, append(append([]byte{}, payload...), 0x02), nil)
	header := make([]byte, 0, 16+4+1+len(senderRaw))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, homePushRecordSize)
	header = append(header, byte(len(senderRaw)))
	header = append(header, senderRaw...)
	return append(header, ciphertext...), nil
}

// homePushVAPIDAuthorization signs the VAPID JWT; its subject is the Hub
// origin (RFC 8292 accepts an https URL).
func homePushVAPIDAuthorization(key *ecdsa.PrivateKey, audience, subject string, now time.Time) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}
	token, err := jwt.Signed(signer).Claims(map[string]any{
		"aud": audience, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject,
	}).Serialize()
	if err != nil {
		return "", err
	}
	public, err := key.PublicKey.ECDH()
	if err != nil {
		return "", err
	}
	return "vapid t=" + token + ", k=" + base64.RawURLEncoding.EncodeToString(public.Bytes()), nil
}

// ensureHomePushVAPIDKey loads the Authority's VAPID key or generates it into
// local custody on first use (no-replace).
func ensureHomePushVAPIDKey(root string, now time.Time) (*ecdsa.PrivateKey, error) {
	if key, err := loadHomePushVAPIDKey(root); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(homePushVAPIDFile{Schema: homePushVAPIDSchema, PKCS8: base64.RawStdEncoding.EncodeToString(der), CreatedAt: now.UTC()})
	if err != nil {
		return nil, err
	}
	if err := writeHomePrivate(root, homePushVAPIDPath, raw, true); err != nil {
		return loadHomePushVAPIDKey(root)
	}
	return loadHomePushVAPIDKey(root)
}

func loadHomePushVAPIDKey(root string) (*ecdsa.PrivateKey, error) {
	raw, err := readHomePrivate(root, homePushVAPIDPath, 4<<10)
	if err != nil {
		return nil, err
	}
	var file homePushVAPIDFile
	if json.Unmarshal(raw, &file) != nil || file.Schema != homePushVAPIDSchema {
		return nil, errors.New("identity: unreadable VAPID custody")
	}
	der, err := base64.RawStdEncoding.DecodeString(file.PKCS8)
	if err != nil {
		return nil, errors.New("identity: unreadable VAPID custody")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	key, ok := parsed.(*ecdsa.PrivateKey)
	if err != nil || !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("identity: unreadable VAPID custody")
	}
	return key, nil
}

// loadHomePushVAPIDPublicKey is the applicationServerKey the Hub subscribes
// with: the uncompressed public point, base64url.
func loadHomePushVAPIDPublicKey(root string) (string, error) {
	key, err := ensureHomePushVAPIDKey(root, time.Now())
	if err != nil {
		return "", err
	}
	public, err := key.PublicKey.ECDH()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(public.Bytes()), nil
}

func loadHomePushSubscriptions(root string) (homePushSubscriptions, error) {
	set := homePushSubscriptions{Schema: homePushSubscriptionsSch}
	raw, err := readHomePrivate(root, homePushSubscriptionsPath, homePushMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return set, err
	}
	if json.Unmarshal(raw, &set) != nil || set.Schema != homePushSubscriptionsSch {
		return homePushSubscriptions{Schema: homePushSubscriptionsSch}, errors.New("identity: unreadable push subscriptions")
	}
	return set, nil
}

func updateHomePushSubscriptions(root string, mutate func(*homePushSubscriptions) error) error {
	return updateHomePrivateJSON(root, homePushSubscriptionsPath, func(raw []byte) ([]byte, error) {
		set := homePushSubscriptions{Schema: homePushSubscriptionsSch}
		if raw != nil && (json.Unmarshal(raw, &set) != nil || set.Schema != homePushSubscriptionsSch) {
			return nil, errors.New("identity: unreadable push subscriptions")
		}
		if err := mutate(&set); err != nil {
			return nil, err
		}
		return json.Marshal(set)
	})
}

// updateHomePrivateJSON serializes one read-modify-write of a custody file
// across goroutines and processes. raw is nil when the file does not exist.
func updateHomePrivateJSON(root, path string, mutate func([]byte) ([]byte, error)) error {
	release, err := localevidence.AcquireCustodyLock(root, path, 101)
	if err != nil {
		return err
	}
	defer release()
	raw, err := readHomePrivate(root, path, homePushMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		raw, err = nil, nil
	}
	if err != nil {
		return err
	}
	next, err := mutate(raw)
	if err != nil {
		return err
	}
	if len(next) > homePushMaxBytes {
		return ErrHomeRegistryFull
	}
	return writeHomePrivate(root, path, next, false)
}
