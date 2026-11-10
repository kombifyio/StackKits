package homegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const maxLoxoneBytes = 16 << 20

// LoxoneRoom is a room of the Miniserver structure file.
type LoxoneRoom struct {
	Name string
	Ref  string // Loxone room uuid
}

// LoxoneDevice is a control of the Miniserver structure file, mapped to an
// external device. Loxone control is not part of the first delivery, so the
// role is always control.
type LoxoneDevice struct {
	Name     string
	Class    string
	Roles    []string
	Ref      string // Loxone control uuid
	RoomRef  string // Loxone room uuid, empty when the control has none
	Type     string // Loxone control type
	Category string // Loxone category name
}

// LoxoneImport is the read-only result of parsing LoxAPP3.json. Loxone has no
// floors.
type LoxoneImport struct {
	Rooms   []LoxoneRoom
	Devices []LoxoneDevice
}

type loxStructure struct {
	Rooms map[string]struct {
		Name string `json:"name"`
	} `json:"rooms"`
	Cats map[string]struct {
		Name string `json:"name"`
	} `json:"cats"`
	Controls map[string]struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Room string `json:"room"`
		Cat  string `json:"cat"`
	} `json:"controls"`
}

// ParseLoxoneStructure reads the bytes of LoxAPP3.json. The result is sorted by
// name and uuid so repeated imports compare equal.
func ParseLoxoneStructure(data []byte) (LoxoneImport, error) {
	var s loxStructure
	if err := json.Unmarshal(data, &s); err != nil {
		return LoxoneImport{}, fmt.Errorf("homegraph: decode Loxone structure: %w", err)
	}
	if s.Rooms == nil && s.Controls == nil {
		return LoxoneImport{}, errors.New("homegraph: not a Loxone structure file (no rooms and no controls)")
	}
	var imp LoxoneImport
	for id, r := range s.Rooms {
		if strings.TrimSpace(r.Name) == "" || !reExtRef.MatchString(id) {
			continue
		}
		imp.Rooms = append(imp.Rooms, LoxoneRoom{Name: clipName(r.Name), Ref: id})
	}
	for id, c := range s.Controls {
		if strings.TrimSpace(c.Name) == "" || !reExtRef.MatchString(id) {
			continue
		}
		d := LoxoneDevice{Name: clipName(c.Name), Ref: id, Type: c.Type, Roles: []string{RoleControl}}
		d.Class = loxoneClass(c.Type)
		if _, ok := s.Rooms[c.Room]; ok {
			d.RoomRef = c.Room
		}
		d.Category = s.Cats[c.Cat].Name
		imp.Devices = append(imp.Devices, d)
	}
	sort.Slice(imp.Rooms, func(i, j int) bool {
		if imp.Rooms[i].Name != imp.Rooms[j].Name {
			return imp.Rooms[i].Name < imp.Rooms[j].Name
		}
		return imp.Rooms[i].Ref < imp.Rooms[j].Ref
	})
	sort.Slice(imp.Devices, func(i, j int) bool {
		if imp.Devices[i].Name != imp.Devices[j].Name {
			return imp.Devices[i].Name < imp.Devices[j].Name
		}
		return imp.Devices[i].Ref < imp.Devices[j].Ref
	})
	return imp, nil
}

func clipName(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= 80 {
		return s
	}
	return string([]rune(s)[:80])
}

func loxoneClass(typ string) string {
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "intercom"):
		return ClassIntercom
	case strings.HasPrefix(t, "audiozone"), strings.Contains(t, "audioplayer"):
		return ClassSpeaker
	case strings.HasPrefix(t, "infoonly"):
		return ClassSensor
	case strings.HasPrefix(t, "lightcontroller"), strings.HasPrefix(t, "lightv"), t == "switch",
		strings.HasPrefix(t, "jalousie"), strings.HasPrefix(t, "dimmer"), strings.HasPrefix(t, "irc"),
		strings.HasPrefix(t, "pushbutton"), strings.HasPrefix(t, "slider"):
		return ClassActuator
	default:
		return ClassOther
	}
}

// MergeLoxone adds the rooms and devices of imp that the graph does not hold
// yet (matched by the loxone external_ref) and returns the new graph. Floors
// stay as set in kombify; existing objects are left untouched.
func MergeLoxone(g Graph, imp LoxoneImport) Graph {
	out := g
	out.Rooms = append([]Room(nil), g.Rooms...)
	out.Devices = append([]Device(nil), g.Devices...)
	roomByRef := map[string]string{}
	for _, r := range out.Rooms {
		for _, e := range r.ExternalRefs {
			if e.System == SystemLoxone {
				roomByRef[e.Ref] = r.ID
			}
		}
	}
	haveDev := map[string]bool{}
	for _, d := range out.Devices {
		for _, e := range d.ExternalRefs {
			if e.System == SystemLoxone {
				haveDev[e.Ref] = true
			}
		}
	}
	// Rooms are unique per household by normalized name, so a Loxone room
	// whose name or alias matches an existing room (or an earlier Loxone room
	// of this import) is attached to it instead of creating a duplicate.
	roomIdx := map[string]int{}
	for i, r := range out.Rooms {
		roomIdx[normName(r.Name)] = i
		for _, a := range r.Aliases {
			if _, ok := roomIdx[normName(a)]; !ok {
				roomIdx[normName(a)] = i
			}
		}
	}
	for _, lr := range imp.Rooms {
		if _, ok := roomByRef[lr.Ref]; ok {
			continue
		}
		if i, ok := roomIdx[normName(lr.Name)]; ok {
			r := out.Rooms[i]
			if len(r.ExternalRefs) < 4 {
				refs := append(append([]ExternalRef(nil), r.ExternalRefs...), ExternalRef{System: SystemLoxone, Ref: lr.Ref})
				out.Rooms[i].ExternalRefs = refs
			}
			roomByRef[lr.Ref] = r.ID
			continue
		}
		id := NewID("rom_")
		out.Rooms = append(out.Rooms, Room{ID: id, Name: lr.Name, Aliases: []string{},
			ExternalRefs: []ExternalRef{{System: SystemLoxone, Ref: lr.Ref}}})
		roomIdx[normName(lr.Name)] = len(out.Rooms) - 1
		roomByRef[lr.Ref] = id
	}
	for _, ld := range imp.Devices {
		if haveDev[ld.Ref] {
			continue
		}
		d := Device{ID: NewID("hdv_"), Name: ld.Name, Class: ld.Class, Roles: append([]string(nil), ld.Roles...),
			Identity:     Identity{Kind: IdentityExt, Ref: "loxone:" + ld.Ref},
			ExternalRefs: []ExternalRef{{System: SystemLoxone, Ref: ld.Ref}}, Status: StatusActive}
		if id, ok := roomByRef[ld.RoomRef]; ok {
			d.RoomID = &id
		}
		out.Devices = append(out.Devices, d)
	}
	return out
}

// FetchLoxoneStructure reads /data/LoxAPP3.json from the Miniserver at baseURL
// (https only) with basic auth. It only reads. The credentials are never
// logged and never part of an error.
func FetchLoxoneStructure(ctx context.Context, baseURL, user, pass string) ([]byte, error) {
	return FetchLoxoneStructureWith(ctx, nil, baseURL, user, pass)
}

// FetchLoxoneStructureWith is FetchLoxoneStructure with a caller-supplied HTTP
// client, for example one that pins the Miniserver's self-signed certificate.
// A nil client uses a default one with a 30 s timeout. Redirects are refused.
func FetchLoxoneStructureWith(ctx context.Context, client *http.Client, baseURL, user, pass string) ([]byte, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("homegraph: invalid Loxone base URL")
	}
	if u.Scheme != "https" {
		return nil, errors.New("homegraph: Loxone base URL must be https")
	}
	u.User = nil
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/") + "/data/LoxAPP3.json"
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("homegraph: build Loxone request")
	}
	req.SetBasicAuth(user, pass)
	resp, err := c.Do(req)
	if err != nil {
		// url.Error carries the URL (no credentials in it), not the headers.
		return nil, fmt.Errorf("homegraph: fetch Loxone structure: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("homegraph: Loxone structure request returned status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxLoxoneBytes+1))
	if err != nil {
		return nil, fmt.Errorf("homegraph: read Loxone structure: %w", err)
	}
	if len(b) > maxLoxoneBytes {
		return nil, errors.New("homegraph: Loxone structure file is too large")
	}
	return b, nil
}
