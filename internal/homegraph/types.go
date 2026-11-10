package homegraph

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// SchemaID is the const of the "schema" property of the home graph document.
const SchemaID = "kombify.home_graph.v1"

// Leading systems.
const (
	LeadingKombify       = "kombify"
	LeadingHomeAssistant = "home_assistant"
)

// External systems.
const (
	SystemHomeAssistant = "home_assistant"
	SystemLoxone        = "loxone"
)

// Device classes.
const (
	ClassServer   = "server"
	ClassDesktop  = "desktop"
	ClassPhone    = "phone"
	ClassTablet   = "tablet"
	ClassWearable = "wearable"
	ClassVehicle  = "vehicle"
	ClassVoice    = "voice_device"
	ClassSpeaker  = "speaker"
	ClassIntercom = "intercom"
	ClassDisplay  = "display"
	ClassCamera   = "camera"
	ClassLock     = "lock"
	ClassAlarm    = "alarm"
	ClassSensor   = "sensor"
	ClassActuator = "actuator"
	ClassOther    = "other"
)

// Device roles.
const (
	RoleAudioIn  = "audio_in"
	RoleAudioOut = "audio_out"
	RoleAnnounce = "announce"
	RoleDisplay  = "display"
	RoleCompute  = "compute"
	RoleControl  = "control"
)

// Identity kinds, device statuses and assignment kinds.
const (
	IdentityEdge    = "edge_device"
	IdentityConnect = "connect_installation"
	IdentityNode    = "techstack_node"
	IdentityExt     = "external"
	StatusActive    = "active"
	StatusRevoked   = "revoked"
	AssignPerson    = "person"
	AssignAnswersOn = "answers_on"
	AssignPartOf    = "part_of"
)

// Graph is the home graph document.
type Graph struct {
	Schema        string       `json:"schema"`
	SpaceID       string       `json:"space_id"`
	Revision      int64        `json:"revision"`
	LeadingSystem string       `json:"leading_system"`
	MirrorClasses []string     `json:"mirror_classes"`
	Floors        []Floor      `json:"floors"`
	Rooms         []Room       `json:"rooms"`
	Devices       []Device     `json:"devices"`
	Assignments   []Assignment `json:"assignments"`
}

// ExternalRef is the identity of the same object in Home Assistant or Loxone.
type ExternalRef struct {
	System string `json:"system"`
	Ref    string `json:"ref"`
}

// Identity references the registry that owns a device's keys.
type Identity struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// Floor is a level of the home.
type Floor struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Level        int           `json:"level"`
	ExternalRefs []ExternalRef `json:"external_refs"`
}

// Room maps to a Home Assistant area and a Loxone room.
type Room struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	FloorID      *string       `json:"floor_id"`
	Icon         string        `json:"icon,omitempty"`
	Aliases      []string      `json:"aliases"`
	ExternalRefs []ExternalRef `json:"external_refs"`
}

// Device is one physical device.
type Device struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Class        string        `json:"class"`
	Roles        []string      `json:"roles"`
	RoomID       *string       `json:"room_id"`
	Identity     Identity      `json:"identity"`
	ExternalRefs []ExternalRef `json:"external_refs"`
	Status       string        `json:"status"`
}

// Assignment is a typed relation of a device.
type Assignment struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	DeviceID       string `json:"device_id"`
	Subject        string `json:"subject,omitempty"`
	TargetDeviceID string `json:"target_device_id,omitempty"`
}

var (
	reSpace    = regexp.MustCompile(`^spc_[a-f0-9]{24}$`)
	reFloor    = regexp.MustCompile(`^flr_[a-f0-9]{24}$`)
	reRoom     = regexp.MustCompile(`^rom_[a-f0-9]{24}$`)
	reDevice   = regexp.MustCompile(`^hdv_[a-f0-9]{24}$`)
	reAssign   = regexp.MustCompile(`^asg_[a-f0-9]{24}$`)
	reSubject  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:|@-]{0,254}$`)
	reIdentRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:|@/-]*$`)
	reExtRef   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	reIcon     = regexp.MustCompile(`^[a-z0-9:-]{1,64}$`)
)

var (
	classes = setOf(ClassServer, ClassDesktop, ClassPhone, ClassTablet, ClassWearable, ClassVehicle, ClassVoice,
		ClassSpeaker, ClassIntercom, ClassDisplay, ClassCamera, ClassLock, ClassAlarm, ClassSensor, ClassActuator, ClassOther)
	roles      = setOf(RoleAudioIn, RoleAudioOut, RoleAnnounce, RoleDisplay, RoleCompute, RoleControl)
	identKinds = setOf(IdentityEdge, IdentityConnect, IdentityNode, IdentityExt)
	extSystems = setOf(SystemHomeAssistant, SystemLoxone)
)

func setOf(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// NewID returns a fresh id with the given prefix ("flr_", "rom_", "hdv_", "asg_").
func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("homegraph: no randomness: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

type validator struct{ errs []error }

func (v *validator) f(format string, a ...any) { v.errs = append(v.errs, fmt.Errorf(format, a...)) }

func (v *validator) name(where, n string) {
	if l := utf8.RuneCountInString(n); l < 1 || l > 80 {
		v.f("%s: name must be 1 to 80 characters", where)
	}
}

func (v *validator) refs(where string, refs []ExternalRef) {
	if len(refs) > 4 {
		v.f("%s: at most 4 external_refs", where)
	}
	for _, r := range refs {
		if !extSystems[r.System] {
			v.f("%s: external_ref system %q", where, r.System)
		}
		if len(r.Ref) < 1 || len(r.Ref) > 255 || !reExtRef.MatchString(r.Ref) {
			v.f("%s: external_ref ref is invalid", where)
		}
	}
}

// Validate enforces the enums, id patterns and referential integrity of the
// home-graph.v1 schema. It reports every problem it finds.
func (g Graph) Validate() error {
	v := &validator{}
	if g.Schema != SchemaID {
		v.f("schema must be %q", SchemaID)
	}
	if !reSpace.MatchString(g.SpaceID) {
		v.f("space_id is invalid")
	}
	if g.Revision < 0 || g.Revision > 9007199254740991 {
		v.f("revision out of range")
	}
	if g.LeadingSystem != LeadingKombify && g.LeadingSystem != LeadingHomeAssistant {
		v.f("leading_system %q", g.LeadingSystem)
	}
	if len(g.MirrorClasses) > 16 {
		v.f("mirror_classes: at most 16")
	}
	seenClass := map[string]bool{}
	for _, c := range g.MirrorClasses {
		if !classes[c] {
			v.f("mirror_classes: class %q", c)
		}
		if seenClass[c] {
			v.f("mirror_classes: duplicate %q", c)
		}
		seenClass[c] = true
	}
	if len(g.Floors) > 32 || len(g.Rooms) > 256 || len(g.Devices) > 1024 || len(g.Assignments) > 4096 {
		v.f("collection exceeds its maximum size")
	}

	floors := map[string]bool{}
	for _, f := range g.Floors {
		w := "floor " + f.ID
		if !reFloor.MatchString(f.ID) {
			v.f("%s: invalid id", w)
		}
		if floors[f.ID] {
			v.f("%s: duplicate id", w)
		}
		floors[f.ID] = true
		v.name(w, f.Name)
		if f.Level < -10 || f.Level > 100 {
			v.f("%s: level out of range", w)
		}
		v.refs(w, f.ExternalRefs)
	}
	rooms := map[string]bool{}
	for _, r := range g.Rooms {
		w := "room " + r.ID
		if !reRoom.MatchString(r.ID) {
			v.f("%s: invalid id", w)
		}
		if rooms[r.ID] {
			v.f("%s: duplicate id", w)
		}
		rooms[r.ID] = true
		v.name(w, r.Name)
		if r.FloorID != nil && !floors[*r.FloorID] {
			v.f("%s: floor_id references no floor", w)
		}
		if r.Icon != "" && !reIcon.MatchString(r.Icon) {
			v.f("%s: invalid icon", w)
		}
		if len(r.Aliases) > 8 {
			v.f("%s: at most 8 aliases", w)
		}
		seen := map[string]bool{}
		for _, a := range r.Aliases {
			v.name(w+" alias", a)
			if seen[a] {
				v.f("%s: duplicate alias", w)
			}
			seen[a] = true
		}
		v.refs(w, r.ExternalRefs)
	}
	devs := map[string]*Device{}
	for i := range g.Devices {
		d := &g.Devices[i]
		w := "device " + d.ID
		if !reDevice.MatchString(d.ID) {
			v.f("%s: invalid id", w)
		}
		if devs[d.ID] != nil {
			v.f("%s: duplicate id", w)
		}
		devs[d.ID] = d
		v.name(w, d.Name)
		if !classes[d.Class] {
			v.f("%s: class %q", w, d.Class)
		}
		if len(d.Roles) > 6 {
			v.f("%s: at most 6 roles", w)
		}
		seen := map[string]bool{}
		for _, ro := range d.Roles {
			if !roles[ro] {
				v.f("%s: role %q", w, ro)
			}
			if seen[ro] {
				v.f("%s: duplicate role %q", w, ro)
			}
			seen[ro] = true
		}
		if d.RoomID != nil && !rooms[*d.RoomID] {
			v.f("%s: room_id references no room", w)
		}
		if !identKinds[d.Identity.Kind] {
			v.f("%s: identity kind %q", w, d.Identity.Kind)
		}
		if l := len(d.Identity.Ref); l < 1 || l > 255 || !reIdentRef.MatchString(d.Identity.Ref) {
			v.f("%s: identity ref is invalid", w)
		}
		if d.Status != StatusActive && d.Status != StatusRevoked {
			v.f("%s: status %q", w, d.Status)
		}
		v.refs(w, d.ExternalRefs)
	}
	ids := map[string]bool{}
	for _, a := range g.Assignments {
		w := "assignment " + a.ID
		if !reAssign.MatchString(a.ID) {
			v.f("%s: invalid id", w)
		}
		if ids[a.ID] {
			v.f("%s: duplicate id", w)
		}
		ids[a.ID] = true
		if devs[a.DeviceID] == nil {
			v.f("%s: device_id references no device", w)
		}
		switch a.Kind {
		case AssignPerson:
			if !reSubject.MatchString(a.Subject) {
				v.f("%s: person needs a valid subject", w)
			}
			if a.TargetDeviceID != "" {
				v.f("%s: person takes no target_device_id", w)
			}
		case AssignAnswersOn, AssignPartOf:
			if a.Subject != "" {
				v.f("%s: %s takes no subject", w, a.Kind)
			}
			t := devs[a.TargetDeviceID]
			if t == nil {
				v.f("%s: target_device_id references no device", w)
				break
			}
			if a.Kind == AssignAnswersOn && !hasRole(t, RoleAudioOut) && !hasRole(t, RoleAnnounce) {
				v.f("%s: answers_on target needs the audio_out or announce role", w)
			}
		default:
			v.f("%s: kind %q", w, a.Kind)
		}
	}
	return errors.Join(v.errs...)
}

func hasRole(d *Device, role string) bool {
	for _, r := range d.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// haRef returns the Home Assistant reference of an external_refs list.
func haRef(refs []ExternalRef) string {
	for _, r := range refs {
		if r.System == SystemHomeAssistant {
			return r.Ref
		}
	}
	return ""
}

// withRef returns refs with the Home Assistant reference set to ref.
func withRef(refs []ExternalRef, system, ref string) []ExternalRef {
	out := make([]ExternalRef, 0, len(refs)+1)
	done := false
	for _, r := range refs {
		if r.System == system {
			if !done {
				out = append(out, ExternalRef{System: system, Ref: ref})
				done = true
			}
			continue
		}
		out = append(out, r)
	}
	if !done {
		out = append(out, ExternalRef{System: system, Ref: ref})
	}
	return out
}
