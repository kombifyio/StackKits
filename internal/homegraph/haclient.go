package homegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	defaultCallTimeout = 30 * time.Second
	maxMessageBytes    = 16 << 20
)

// HAFloor is a Home Assistant floor registry entry.
type HAFloor struct {
	FloorID string   `json:"floor_id"`
	Name    string   `json:"name"`
	Level   *int     `json:"level"`
	Aliases []string `json:"aliases"`
	Icon    *string  `json:"icon"`
}

// HAArea is a Home Assistant area registry entry.
type HAArea struct {
	AreaID  string   `json:"area_id"`
	Name    string   `json:"name"`
	FloorID *string  `json:"floor_id"`
	Labels  []string `json:"labels"`
	Aliases []string `json:"aliases"`
}

// HALabel is a Home Assistant label registry entry.
type HALabel struct {
	LabelID string `json:"label_id"`
	Name    string `json:"name"`
}

// HADevice is a Home Assistant device registry entry.
type HADevice struct {
	ID           string   `json:"id"`
	Name         *string  `json:"name"`
	NameByUser   *string  `json:"name_by_user"`
	AreaID       *string  `json:"area_id"`
	Labels       []string `json:"labels"`
	Manufacturer *string  `json:"manufacturer"`
	Model        *string  `json:"model"`
}

// DisplayName is the name the person sees in Home Assistant.
func (d HADevice) DisplayName() string {
	if d.NameByUser != nil && *d.NameByUser != "" {
		return *d.NameByUser
	}
	if d.Name != nil {
		return *d.Name
	}
	return d.ID
}

// HASnapshot is one read of the four registries.
type HASnapshot struct {
	Floors  []HAFloor
	Areas   []HAArea
	Labels  []HALabel
	Devices []HADevice
}

// Registry is the Home Assistant registry surface the sync needs; *Client
// implements it.
type Registry interface {
	Snapshot(ctx context.Context) (HASnapshot, error)
	CreateFloor(ctx context.Context, name string, level int) (HAFloor, error)
	UpdateFloor(ctx context.Context, id string, name *string, level *int) error
	CreateArea(ctx context.Context, name string, floorID *string, labels []string) (HAArea, error)
	UpdateArea(ctx context.Context, id string, set AreaUpdate) error
	CreateLabel(ctx context.Context, name string) (HALabel, error)
	SetDeviceArea(ctx context.Context, deviceID, areaID string) error
}

// AreaUpdate lists the fields of an area update; nil fields stay untouched.
// ClearFloor sets floor_id to null.
type AreaUpdate struct {
	Name       *string
	FloorID    *string
	ClearFloor bool
	Labels     []string
	SetLabels  bool
}

// Client is a Home Assistant WebSocket API client. It serializes calls; it is
// safe for concurrent use. The access token lives only inside the client.
type Client struct {
	token string
	mu    sync.Mutex
	conn  *websocket.Conn
	next  int64
}

type haMessage struct {
	ID      int64           `json:"id,omitempty"`
	Type    string          `json:"type"`
	Success *bool           `json:"success,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

// NewClient connects to the Home Assistant WebSocket API at origin (the
// http:// or https:// base URL of the instance) over httpClient and
// authenticates with the access token. The destination is decided by
// httpClient: on a node it is the confined Home Assistant transport, so no
// further address rule applies here. Only the scheme and host are checked.
func NewClient(ctx context.Context, httpClient *http.Client, origin, token string) (*Client, error) {
	if token == "" {
		return nil, errors.New("homegraph: empty Home Assistant token")
	}
	if httpClient == nil {
		return nil, errors.New("homegraph: HTTP client is required")
	}
	wsURL, err := websocketURL(origin)
	if err != nil {
		return nil, err
	}
	c := &Client{token: token}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return nil, c.redact(fmt.Errorf("homegraph: connect Home Assistant: %w", err))
	}
	conn.SetReadLimit(maxMessageBytes)
	c.conn = conn
	if err := c.authenticate(ctx); err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	return c, nil
}

// websocketURL derives the WebSocket API URL from the instance origin.
func websocketURL(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil {
		return "", errors.New("homegraph: invalid Home Assistant origin")
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", errors.New("homegraph: Home Assistant origin must be http:// or https://")
	}
	u.Path, u.RawQuery, u.Fragment = "/api/websocket", "", ""
	return u.String(), nil
}

func (c *Client) authenticate(ctx context.Context) error {
	ctx, cancel := callContext(ctx)
	defer cancel()
	var m haMessage
	if err := wsjson.Read(ctx, c.conn, &m); err != nil {
		return c.redact(fmt.Errorf("homegraph: read auth_required: %w", err))
	}
	if m.Type != "auth_required" {
		return fmt.Errorf("homegraph: unexpected first message %q", m.Type)
	}
	if err := wsjson.Write(ctx, c.conn, map[string]string{"type": "auth", "access_token": c.token}); err != nil {
		return c.redact(fmt.Errorf("homegraph: send auth: %w", err))
	}
	if err := wsjson.Read(ctx, c.conn, &m); err != nil {
		return c.redact(fmt.Errorf("homegraph: read auth result: %w", err))
	}
	switch m.Type {
	case "auth_ok":
		return nil
	case "auth_invalid":
		return errors.New("homegraph: Home Assistant rejected the access token")
	default:
		return fmt.Errorf("homegraph: unexpected auth reply %q", m.Type)
	}
}

// callContext gives one exchange its deadline: the caller's, or the default.
// callContext bounds one call to defaultCallTimeout, or less when the caller
// deadline is sooner: a hung call never holds a whole command budget.
func callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, defaultCallTimeout)
}

// redact removes the token from an error text.
func (c *Client) redact(err error) error {
	if err == nil || c.token == "" || !strings.Contains(err.Error(), c.token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "[redacted]"))
}

// Close closes the connection. It does not wait for an in-flight call, which
// fails instead.
func (c *Client) Close() error { return c.conn.Close(websocket.StatusNormalClosure, "") }

// Call sends one command and decodes its result into out (nil to discard).
func (c *Client) Call(ctx context.Context, typ string, params map[string]any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := callContext(ctx)
	defer cancel()
	c.next++
	id := c.next
	req := map[string]any{"id": id, "type": typ}
	for k, v := range params {
		req[k] = v
	}
	if err := wsjson.Write(ctx, c.conn, req); err != nil {
		return c.redact(fmt.Errorf("homegraph: send %s: %w", typ, err))
	}
	for {
		var m haMessage
		if err := wsjson.Read(ctx, c.conn, &m); err != nil {
			return c.redact(fmt.Errorf("homegraph: read %s: %w", typ, err))
		}
		if m.ID != id || m.Type != "result" {
			continue
		}
		if m.Success != nil && !*m.Success {
			code, msg := "unknown", ""
			if m.Error != nil {
				code, msg = m.Error.Code, m.Error.Message
			}
			return c.redact(fmt.Errorf("homegraph: %s failed: %s %s", typ, code, msg))
		}
		if out == nil || len(m.Result) == 0 || string(m.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(m.Result, out); err != nil {
			return fmt.Errorf("homegraph: decode %s: %w", typ, err)
		}
		return nil
	}
}

// Snapshot reads the floor, area, label and device registries.
func (c *Client) Snapshot(ctx context.Context) (HASnapshot, error) {
	var s HASnapshot
	for _, q := range []struct {
		typ string
		out any
	}{
		{"config/floor_registry/list", &s.Floors},
		{"config/area_registry/list", &s.Areas},
		{"config/label_registry/list", &s.Labels},
		{"config/device_registry/list", &s.Devices},
	} {
		if err := c.Call(ctx, q.typ, nil, q.out); err != nil {
			return HASnapshot{}, err
		}
	}
	return s, nil
}

// CreateFloor creates a floor.
func (c *Client) CreateFloor(ctx context.Context, name string, level int) (HAFloor, error) {
	var f HAFloor
	err := c.Call(ctx, "config/floor_registry/create", map[string]any{"name": name, "level": level}, &f)
	return f, err
}

// UpdateFloor renames a floor or changes its level; nil fields stay untouched.
func (c *Client) UpdateFloor(ctx context.Context, id string, name *string, level *int) error {
	p := map[string]any{"floor_id": id}
	if name != nil {
		p["name"] = *name
	}
	if level != nil {
		p["level"] = *level
	}
	return c.Call(ctx, "config/floor_registry/update", p, nil)
}

// CreateArea creates an area.
func (c *Client) CreateArea(ctx context.Context, name string, floorID *string, labels []string) (HAArea, error) {
	p := map[string]any{"name": name}
	if floorID != nil {
		p["floor_id"] = *floorID
	}
	if len(labels) > 0 {
		p["labels"] = labels
	}
	var a HAArea
	err := c.Call(ctx, "config/area_registry/create", p, &a)
	return a, err
}

// UpdateArea updates an area.
func (c *Client) UpdateArea(ctx context.Context, id string, u AreaUpdate) error {
	p := map[string]any{"area_id": id}
	if u.Name != nil {
		p["name"] = *u.Name
	}
	if u.FloorID != nil {
		p["floor_id"] = *u.FloorID
	} else if u.ClearFloor {
		p["floor_id"] = nil
	}
	if u.SetLabels {
		labels := u.Labels
		if labels == nil {
			labels = []string{}
		}
		p["labels"] = labels
	}
	return c.Call(ctx, "config/area_registry/update", p, nil)
}

// CreateLabel creates a label.
func (c *Client) CreateLabel(ctx context.Context, name string) (HALabel, error) {
	var l HALabel
	err := c.Call(ctx, "config/label_registry/create", map[string]any{"name": name}, &l)
	return l, err
}

// SetDeviceArea places a device in an area.
func (c *Client) SetDeviceArea(ctx context.Context, deviceID, areaID string) error {
	return c.Call(ctx, "config/device_registry/update", map[string]any{"device_id": deviceID, "area_id": areaID}, nil)
}

var _ Registry = (*Client)(nil)
