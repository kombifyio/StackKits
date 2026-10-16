package appsetup

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// HomeAssistantMQTTRequest is derived from the applied broker bundle and its
// node custody. Password is transient and never part of setup evidence.
type HomeAssistantMQTTRequest struct {
	Broker   string
	Port     int
	Username string
	Password string `json:"-"`
}

type HomeAssistantMQTTEvidence struct {
	EntryID                 string `json:"entryId"`
	Created                 bool   `json:"created"`
	ExistingPreserved       bool   `json:"existingPreserved"`
	BrokerRoundTripVerified bool   `json:"brokerRoundTripVerified"`
}

type homeAssistantMQTTEntry struct {
	ID     string `json:"entry_id"`
	Domain string `json:"domain"`
	State  string `json:"state"`
}

type homeAssistantMQTTFlow struct {
	ID     string                 `json:"flow_id"`
	Type   string                 `json:"type"`
	Step   string                 `json:"step_id"`
	Result homeAssistantMQTTEntry `json:"result"`
	Schema []struct {
		Name        string          `json:"name"`
		Default     json.RawMessage `json:"default"`
		Description struct {
			Suggested json.RawMessage `json:"suggested_value"`
		} `json:"description"`
	} `json:"data_schema"`
}

// configureHomeAssistantMQTT uses HA's own config flow, which authenticates to
// the broker before creating an entry. An existing entry is inspected through
// a reconfigure form and left untouched, including discovery and TLS options.
func configureHomeAssistantMQTT(ctx context.Context, client *http.Client, baseURL, token string, request HomeAssistantMQTTRequest) (evidence HomeAssistantMQTTEvidence, returnErr error) {
	if request.Broker == "" || request.Port < 1 || request.Port > 65535 || request.Username == "" || request.Password == "" {
		return evidence, errors.New("Home Assistant MQTT setup requires the applied broker and node-custodied credentials")
	}
	readEntries := func() ([]homeAssistantMQTTEntry, error) {
		var entries []homeAssistantMQTTEntry
		err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/config/config_entries/entry", nil, token, &entries)
		var mqtt []homeAssistantMQTTEntry
		for _, entry := range entries {
			if entry.Domain == "mqtt" {
				mqtt = append(mqtt, entry)
			}
		}
		return mqtt, err
	}
	entries, err := readEntries()
	if err != nil {
		return evidence, errors.New("Home Assistant MQTT entry readback failed")
	}
	if len(entries) > 1 {
		return evidence, errors.New("Home Assistant has multiple MQTT entries; preserve them and select the intended integration explicitly")
	}
	payload := map[string]string{"handler": "mqtt"}
	if len(entries) == 1 {
		payload["entry_id"] = entries[0].ID
	}
	var flow homeAssistantMQTTFlow
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/api/config/config_entries/flow", payload, token, &flow); err != nil {
		return evidence, errors.New("Home Assistant MQTT config flow could not start")
	}
	flowID := flow.ID
	defer func() {
		if flowID == "" {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), homeAssistantCleanupTimeout)
		defer cancel()
		err := homeAssistantJSONRequest(cleanupCtx, client, baseURL, http.MethodDelete, "/api/config/config_entries/flow/"+url.PathEscape(flowID), nil, token, nil)
		var statusErr *homeAssistantHTTPError
		if err != nil && !(errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound) {
			evidence = HomeAssistantMQTTEvidence{}
			returnErr = errors.Join(returnErr, errors.New("Home Assistant MQTT setup flow cleanup failed"))
		}
	}()
	if flow.Type != "form" || flow.Step != "broker" || flow.ID == "" {
		return evidence, errors.New("Home Assistant returned an unsupported MQTT broker flow")
	}
	if len(entries) == 1 {
		values := make(map[string]json.RawMessage)
		for _, field := range flow.Schema {
			value := field.Default
			if len(value) == 0 {
				value = field.Description.Suggested
			}
			values[field.Name] = value
		}
		var broker, username string
		var port int
		if json.Unmarshal(values["broker"], &broker) != nil || json.Unmarshal(values["port"], &port) != nil || json.Unmarshal(values["username"], &username) != nil || broker != request.Broker || port != request.Port || username != request.Username {
			return evidence, errors.New("Home Assistant's existing MQTT integration differs from the applied broker; it was preserved")
		}
		evidence = HomeAssistantMQTTEvidence{EntryID: entries[0].ID, ExistingPreserved: true}
	} else {
		if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/api/config/config_entries/flow/"+url.PathEscape(flowID), map[string]any{"broker": request.Broker, "port": request.Port, "username": request.Username, "password": request.Password}, token, &flow); err != nil {
			return evidence, errors.New("Home Assistant MQTT broker setup failed")
		}
		if flow.Type != "create_entry" || flow.Result.ID == "" {
			return evidence, errors.New("Home Assistant could not authenticate to the applied MQTT broker")
		}
		flowID = "" // HA completed this flow; it no longer exists.
		evidence = HomeAssistantMQTTEvidence{EntryID: flow.Result.ID, Created: true}
	}
	for {
		entries, err = readEntries()
		if err != nil {
			return HomeAssistantMQTTEvidence{}, errors.New("Home Assistant MQTT setup readback failed")
		}
		for _, entry := range entries {
			if entry.ID == evidence.EntryID && entry.State == "loaded" {
				if err := verifyHomeAssistantMQTTRoundTrip(ctx, client, baseURL, token); err != nil {
					return HomeAssistantMQTTEvidence{}, err
				}
				evidence.BrokerRoundTripVerified = true
				return evidence, nil
			}
		}
		select {
		case <-ctx.Done():
			return HomeAssistantMQTTEvidence{}, fmt.Errorf("Home Assistant MQTT integration did not load: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// An unretained, random private topic proves the configured HA client actually
// traverses the broker. A loaded entry alone can still have a disconnected client.
func verifyHomeAssistantMQTTRoundTrip(ctx context.Context, client *http.Client, baseURL, token string) error {
	ctx, cancel := context.WithTimeout(ctx, homeAssistantWebSocketTimeout)
	defer cancel()
	endpoint, err := homeAssistantWebSocketURL(baseURL)
	if err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return errors.New("Home Assistant MQTT readback connection failed")
	}
	defer conn.Close(websocket.StatusNormalClosure, "MQTT readback complete")
	conn.SetReadLimit(homeAssistantWSLimit)
	var greeting homeAssistantWSGreeting
	if err := wsjson.Read(ctx, conn, &greeting); err != nil || greeting.Type != "auth_required" || greeting.HAVersion != HomeAssistantPinnedVersion {
		return errors.New("Home Assistant MQTT readback version changed")
	}
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "auth", "access_token": token}); err != nil {
		return errors.New("Home Assistant MQTT readback authentication failed")
	}
	var auth homeAssistantWSAuthResult
	if err := wsjson.Read(ctx, conn, &auth); err != nil || auth.Type != "auth_ok" || auth.HAVersion != HomeAssistantPinnedVersion {
		return errors.New("Home Assistant MQTT readback authentication failed")
	}
	topic, payload := "stackkit/setup/"+rand.Text(), rand.Text()
	if err := wsjson.Write(ctx, conn, map[string]any{"id": 1, "type": "mqtt/subscribe", "topic": topic, "qos": 1}); err != nil {
		return errors.New("Home Assistant MQTT subscribe failed")
	}
	var subscribed homeAssistantWSResult
	if err := wsjson.Read(ctx, conn, &subscribed); err != nil || subscribed.ID != 1 || subscribed.Type != "result" || !subscribed.Success {
		return errors.New("Home Assistant MQTT subscribe was not accepted")
	}
	type mqttEvent struct {
		ID    int    `json:"id"`
		Type  string `json:"type"`
		Event struct {
			Topic   string `json:"topic"`
			Payload string `json:"payload"`
		} `json:"event"`
	}
	type readback struct {
		event mqttEvent
		err   error
	}
	read := make(chan readback, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var event mqttEvent
		err := wsjson.Read(ctx, conn, &event)
		read <- readback{event: event, err: err}
	}()
	defer func() { _ = conn.CloseNow(); <-done }()
	// HA acknowledges the WebSocket subscription before its debounced broker
	// SUBSCRIBE is sent (initial cooldown 500ms at the governed pin). Repeat
	// only this unretained probe until its actual delivery is observed.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/api/services/mqtt/publish", map[string]any{"topic": topic, "payload": payload, "qos": 1, "retain": false}, token, nil); err != nil {
			return errors.New("Home Assistant MQTT publish failed")
		}
		select {
		case result := <-read:
			if result.err == nil && result.event.ID == 1 && result.event.Type == "event" && result.event.Event.Topic == topic && result.event.Event.Payload == payload {
				return nil
			}
			return errors.New("Home Assistant MQTT broker round trip did not complete")
		case <-ctx.Done():
			return errors.New("Home Assistant MQTT broker round trip did not complete")
		case <-ticker.C:
		}
	}
}
