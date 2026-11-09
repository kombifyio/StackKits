package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Owner step-up requests carry a console action that changes identity custody
// (homelab identity target §9, §10, §12: rotating the recovery key, issuing a
// new recovery code) to the Owner's passkey. The console raises the request
// with the action digest and a fresh challenge; the Owner approves it in the
// Hub on an enrolled Owner device with a passkey sign-in whose nonce binds
// exactly that action and challenge. The Hub only delivers the ID token: the
// console itself verifies it (VerifyHomeStepUp, single use, at most 60 s old)
// before it acts, so a server that stored a token decides nothing.
const (
	HomeStepUpRequestsPath  = "/api/v1/identity/home/step-up/requests"
	homeStepUpRequestsDir   = ".stackkit/custody/home-verifier/step-up-requests"
	homeStepUpRequestTTL    = 5 * time.Minute
	homeStepUpRequestsLimit = 16
)

var (
	homeStepUpRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{24}$`)
	// ErrHomeStepUpRequestExpired ends a console wait without an approval.
	ErrHomeStepUpRequestExpired = errors.New("identity: the Owner did not approve the step-up in time")
)

// HomeOwnerStepUpRequest is one pending console action awaiting the Owner's
// passkey step-up. Summary is the text the Hub shows the Owner.
type HomeOwnerStepUpRequest struct {
	ID        string           `json:"id"`
	Summary   string           `json:"summary"`
	Action    HomeStepUpAction `json:"stepUp"`
	Challenge string           `json:"challenge"`
	CreatedAt time.Time        `json:"createdAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
	IDToken   string           `json:"idToken,omitempty"`
}

// RequestHomeOwnerStepUp records a console action for the Owner's approval.
func RequestHomeOwnerStepUp(root string, action HomeStepUpAction, summary string, now time.Time) (HomeOwnerStepUpRequest, error) {
	if !validHomeStepUpClass(action.Class) || !validHomeDigest(action.Digest) || summary == "" || len(summary) > 200 {
		return HomeOwnerStepUpRequest{}, ErrHomeStepUpDenied
	}
	if _, err := loadCurrentHomeTrust(root, now); err != nil {
		return HomeOwnerStepUpRequest{}, errors.New("identity: no current Home trust; run 'stackkit device trust' on the Authority first")
	}
	id, err := homeRandomID()
	if err != nil {
		return HomeOwnerStepUpRequest{}, err
	}
	challenge, err := homeRandomID()
	if err != nil {
		return HomeOwnerStepUpRequest{}, err
	}
	challenge += id // 48 characters, inside the 32 to 128 challenge bounds
	request := HomeOwnerStepUpRequest{ID: id, Summary: summary, Action: action, Challenge: challenge, CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(homeStepUpRequestTTL)}
	if open, err := listHomeOwnerStepUpRequests(root, now); err == nil && len(open) >= homeStepUpRequestsLimit {
		return HomeOwnerStepUpRequest{}, ErrHomeEnrollmentRateLimited
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return HomeOwnerStepUpRequest{}, err
	}
	return request, writeHomePrivate(root, homeStepUpRequestsDir+"/"+id+".json", raw, true)
}

// AwaitHomeOwnerStepUp waits until the Owner delivered a step-up for request
// and verifies it here: the pinned Owner's passkey through the device sign-in
// client, for exactly this action and challenge, at most 60 seconds old and
// never used before. The request is removed whatever the outcome.
func AwaitHomeOwnerStepUp(ctx context.Context, root string, request HomeOwnerStepUpRequest, poll time.Duration) (string, error) {
	return awaitHomeOwnerStepUp(ctx, root, request, poll, false)
}

// WaitHomeOwnerStepUp is AwaitHomeOwnerStepUp for a caller that is dispatched
// again (the Guard's agent link delivery): when ctx ends before the Owner
// answered, the request stays open for the rest of its life, so the Owner can
// still approve it and the next dispatch verifies the approval.
func WaitHomeOwnerStepUp(ctx context.Context, root string, request HomeOwnerStepUpRequest, poll time.Duration) (string, error) {
	return awaitHomeOwnerStepUp(ctx, root, request, poll, true)
}

// OpenHomeOwnerStepUpRequest returns the open request for exactly this
// action, so a repeated dispatch waits on the request the Owner sees instead
// of raising another one.
func OpenHomeOwnerStepUpRequest(root string, action HomeStepUpAction, now time.Time) (HomeOwnerStepUpRequest, bool) {
	requests, err := listHomeOwnerStepUpRequests(root, now)
	if err != nil {
		return HomeOwnerStepUpRequest{}, false
	}
	for _, request := range requests {
		if request.Action == action {
			return request, true
		}
	}
	return HomeOwnerStepUpRequest{}, false
}

func awaitHomeOwnerStepUp(ctx context.Context, root string, request HomeOwnerStepUpRequest, poll time.Duration, keepOpen bool) (string, error) {
	answered := false
	defer func() {
		if answered || !keepOpen || !time.Now().Before(request.ExpiresAt) {
			removeHomeOwnerStepUpRequest(root, request.ID)
		}
	}()
	for {
		current, err := loadHomeOwnerStepUpRequest(root, request.ID)
		if err != nil {
			return "", err
		}
		if current.IDToken != "" {
			answered = true
			trust, trustErr := loadCurrentHomeTrust(root, time.Now())
			if trustErr != nil {
				return "", ErrHomeStepUpDenied
			}
			return VerifyHomeStepUp(root, request.Action, HomeStepUpApprover{Account: trust.PocketID.Subject}, request.Challenge, current.IDToken, time.Now())
		}
		if !time.Now().Before(request.ExpiresAt) {
			return "", ErrHomeStepUpRequestExpired
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

// registerHomeStepUpRequestRoutes mounts the Owner device's approval routes:
//
//	GET  HomeStepUpRequestsPath        open requests (without any token)
//	POST HomeStepUpRequestsPath/{id}   {"stepUp": "<ID token>"} for one request
//
// Both need an active device of the Owner's account and its proof.
func registerHomeStepUpRequestRoutes(mux *http.ServeMux, root string) {
	mux.Handle("GET "+HomeStepUpRequestsPath, homeDeviceAuthenticated(root, func(w http.ResponseWriter, _ *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		if !actor.Owner {
			writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"requests": []HomeOwnerStepUpRequest{}})
			return
		}
		requests, err := listHomeOwnerStepUpRequests(root, time.Now())
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		for index := range requests {
			requests[index].IDToken = ""
		}
		writeHomeIdentityJSON(w, http.StatusOK, map[string]any{"requests": requests})
	}))
	mux.Handle("POST "+HomeStepUpRequestsPath+"/{id}", homeDeviceAuthenticated(root, func(w http.ResponseWriter, r *http.Request, _ *HomeVerifier, _ HomeAuthentication, actor HomeActor) {
		var body struct {
			StepUp string `json:"stepUp"`
		}
		id := r.PathValue("id")
		if !actor.Owner || !homeStepUpRequestID.MatchString(id) {
			denyHomeIdentity(w, http.StatusNotFound, "not_found", false)
			return
		}
		if !decodeHomeJSON(r.Body, homeIdentityMaxBodyBytes, &body, false) || body.StepUp == "" || len(body.StepUp) > homeVerifierMaxProofBytes {
			denyHomeIdentity(w, http.StatusBadRequest, "invalid_request", false)
			return
		}
		request, err := loadHomeOwnerStepUpRequest(root, id)
		if err != nil || request.IDToken != "" || !time.Now().Before(request.ExpiresAt) {
			denyHomeIdentity(w, http.StatusNotFound, "not_found", false)
			return
		}
		request.IDToken = body.StepUp
		raw, err := json.Marshal(request)
		if err == nil {
			err = writeHomePrivate(root, homeStepUpRequestsDir+"/"+id+".json", raw, false)
		}
		if err != nil {
			denyHomeIdentity(w, http.StatusServiceUnavailable, "registry_unavailable", true)
			return
		}
		writeHomeIdentityJSON(w, http.StatusAccepted, map[string]any{"delivered": true})
	}))
}

func loadHomeOwnerStepUpRequest(root, id string) (HomeOwnerStepUpRequest, error) {
	if !homeStepUpRequestID.MatchString(id) {
		return HomeOwnerStepUpRequest{}, fs.ErrNotExist
	}
	raw, err := readHomePrivate(root, homeStepUpRequestsDir+"/"+id+".json", 64<<10)
	if err != nil {
		return HomeOwnerStepUpRequest{}, err
	}
	var request HomeOwnerStepUpRequest
	if json.Unmarshal(raw, &request) != nil || request.ID != id || !validHomeStepUpChallenge(request.Challenge) || !validHomeDigest(request.Action.Digest) {
		return HomeOwnerStepUpRequest{}, errors.New("identity: unreadable step-up request")
	}
	return request, nil
}

func listHomeOwnerStepUpRequests(root string, now time.Time) ([]HomeOwnerStepUpRequest, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(homeStepUpRequestsDir)))
	if errors.Is(err, fs.ErrNotExist) {
		return []HomeOwnerStepUpRequest{}, nil
	}
	if err != nil {
		return nil, err
	}
	requests := []HomeOwnerStepUpRequest{}
	for _, entry := range entries {
		id, ok := cutSuffix(entry.Name(), ".json")
		if !ok || !homeStepUpRequestID.MatchString(id) {
			continue
		}
		request, loadErr := loadHomeOwnerStepUpRequest(root, id)
		if loadErr != nil {
			continue
		}
		if !now.Before(request.ExpiresAt) {
			removeHomeOwnerStepUpRequest(root, id)
			continue
		}
		requests = append(requests, request)
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].CreatedAt.Before(requests[j].CreatedAt) })
	return requests, nil
}

func removeHomeOwnerStepUpRequest(root, id string) {
	if homeStepUpRequestID.MatchString(id) {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(homeStepUpRequestsDir), id+".json"))
	}
}

func cutSuffix(value, suffix string) (string, bool) {
	if len(value) <= len(suffix) || value[len(value)-len(suffix):] != suffix {
		return "", false
	}
	return value[:len(value)-len(suffix)], true
}
