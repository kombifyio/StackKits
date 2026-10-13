package appsetup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const NtfyLiveDeliveryAction = "ntfy-live-delivery-check"

type NtfyLiveDeliveryResult struct {
	Delivered    bool
	ReplayAbsent bool
	ProofDigest  string
}

// VerifyNtfyLiveDelivery uses the admitted node-local application transport.
// It does not authenticate an upstream account or configure a durable sink.
func VerifyNtfyLiveDelivery(ctx context.Context, client *http.Client, baseURL string) (NtfyLiveDeliveryResult, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") {
		return NtfyLiveDeliveryResult{}, errors.New("invalid ntfy application endpoint")
	}
	if client == nil {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy requires its admitted application transport")
	}
	local := *client
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("cannot create ntfy delivery proof")
	}
	marker := hex.EncodeToString(nonce)
	topic := "stackkit-check-" + marker
	root := strings.TrimRight(base.String(), "/") + "/" + topic
	sub, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/json", nil)
	if err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("cannot prepare ntfy subscription")
	}
	resp, err := local.Do(sub)
	if err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy live subscription failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy refused live subscription")
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	var event struct {
		Event   string `json:"event"`
		Topic   string `json:"topic"`
		Message string `json:"message"`
	}
	if decoder.Decode(&event) != nil || event.Event != "open" || event.Topic != topic {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy did not establish the requested subscription")
	}
	publish, err := http.NewRequestWithContext(ctx, http.MethodPost, root, bytes.NewBufferString(marker))
	if err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("cannot prepare ntfy delivery check")
	}
	publish.Header.Set("Content-Type", "text/plain")
	posted, err := local.Do(publish)
	if err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy publish failed")
	}
	io.Copy(io.Discard, io.LimitReader(posted.Body, 64<<10))
	posted.Body.Close()
	if posted.StatusCode != http.StatusOK {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy refused the delivery check")
	}
	delivered := false
	for !delivered {
		event.Event, event.Topic, event.Message = "", "", ""
		if decoder.Decode(&event) != nil {
			return NtfyLiveDeliveryResult{}, errors.New("ntfy live delivery was not observed")
		}
		delivered = event.Event == "message" && event.Topic == topic && event.Message == marker
	}
	resp.Body.Close()
	poll, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/json?poll=1", nil)
	if err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("cannot prepare ntfy replay check")
	}
	polled, err := local.Do(poll)
	if err != nil {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy replay check failed")
	}
	defer polled.Body.Close()
	if polled.StatusCode != http.StatusOK {
		return NtfyLiveDeliveryResult{}, errors.New("ntfy refused the replay check")
	}
	replay := json.NewDecoder(io.LimitReader(polled.Body, 64<<10))
	for {
		event.Event, event.Topic, event.Message = "", "", ""
		err = replay.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return NtfyLiveDeliveryResult{}, errors.New("ntfy replay response is invalid")
		}
		if event.Event == "message" {
			return NtfyLiveDeliveryResult{}, errors.New("ntfy retained a message in the explicit live-only mode")
		}
	}
	sum := sha256.Sum256([]byte(marker))
	return NtfyLiveDeliveryResult{Delivered: true, ReplayAbsent: true, ProofDigest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}
