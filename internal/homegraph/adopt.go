package homegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Side names the system whose value a decision keeps.
type Side string

// Sides.
const (
	SideKombify Side = "kombify"
	SideHA      Side = "home_assistant"
)

// ConflictKind names a contradiction between kombify and Home Assistant.
type ConflictKind string

// Conflict kinds (HOME-GRAPH-TARGET.md section 8).
const (
	ConflictRoomFloorMismatch  ConflictKind = "room_floor_mismatch"
	ConflictDeviceRoomMismatch ConflictKind = "device_room_mismatch"
	ConflictNameCollision      ConflictKind = "name_collision"
	ConflictDeletedOnOneSide   ConflictKind = "deleted_on_one_side"
)

// Match pairs one kombify object with the Home Assistant object it equals.
type Match struct {
	Kind      string // floor, room, device
	KombifyID string
	HAID      string
	Name      string
}

// Only is an object that exists on one side only; ID is a kombify id for
// OnlyKombify and a Home Assistant id for OnlyHA.
type Only struct {
	Kind string
	ID   string
	Name string
}

// Conflict carries both sides' values so a UI can let the person choose.
type Conflict struct {
	ID        string
	Kind      ConflictKind
	Subject   string // floor, room, device
	KombifyID string
	HAID      string
	Name      string
	// Kombify and HA are the contradicting values as the person reads them
	// (floor name, room name, or the object's name for the other kinds).
	Kombify string
	HA      string
	// Candidates are the Home Assistant ids a name_collision is ambiguous
	// between.
	Candidates []string
	// DeletedOn is the side that no longer has the object
	// (deleted_on_one_side only).
	DeletedOn Side
}

// AdoptionPlan is the inventory, match and conflict list of an adoption run.
// Nothing is written until the person confirms the plan and Resolve produces
// the projection.
type AdoptionPlan struct {
	Graph    Graph
	Snapshot HASnapshot
	Mapping  Mapping
	FirstRun bool
	// Leading is the leading system the person chose; it starts as the
	// graph's setting (kombify by default) and decides every conflict without
	// an explicit decision.
	Leading string

	Matched     []Match
	OnlyKombify []Only
	OnlyHA      []Only
	Conflicts   []Conflict
}

type objK struct {
	id, name, ref string
	aliases       []string
}

type objH struct {
	id, name string
	aliases  []string
}

type matchRes struct {
	pairs     [][2]string
	coll      map[string][]string
	collOrder []string
	delHA     []string // kombify ids whose mapped HA object is gone
	delK      []string // HA ids whose kombify object is gone
	onlyK     []string
	onlyH     []string
}

func keysOf(name string, aliases []string) map[string]bool {
	m := map[string]bool{normName(name): true}
	for _, a := range aliases {
		if n := normName(a); n != "" {
			m[n] = true
		}
	}
	return m
}

func matchObjects(ks []objK, hs []objH, mapped map[string]string, firstRun, byName bool) matchRes {
	res := matchRes{coll: map[string][]string{}}
	hIdx := make(map[string]objH, len(hs))
	for _, h := range hs {
		hIdx[h.id] = h
	}
	kIDs := map[string]bool{}
	for _, k := range ks {
		kIDs[k.id] = true
	}
	rev := map[string]string{}
	for k, h := range mapped {
		rev[h] = k
	}
	used := map[string]bool{}
	collided := map[string]bool{}
	var pending []objK
	for _, k := range ks {
		hid := mapped[k.id]
		fromMap := hid != ""
		if hid == "" {
			hid = k.ref
		}
		if hid != "" {
			_, ok := hIdx[hid]
			if ok && !used[hid] {
				res.pairs = append(res.pairs, [2]string{k.id, hid})
				used[hid] = true
				continue
			}
			if fromMap && !firstRun && !ok {
				res.delHA = append(res.delHA, k.id)
				continue
			}
		}
		pending = append(pending, k)
	}
	if byName {
		count := map[string]int{}
		for _, k := range pending {
			count[normName(k.name)]++
		}
		for _, k := range pending {
			kk := keysOf(k.name, k.aliases)
			var cands []string
			for _, h := range hs {
				if used[h.id] {
					continue
				}
				for key := range keysOf(h.name, h.aliases) {
					if kk[key] {
						cands = append(cands, h.id)
						break
					}
				}
			}
			switch {
			case len(cands) == 0:
				res.onlyK = append(res.onlyK, k.id)
			case len(cands) == 1 && count[normName(k.name)] == 1:
				res.pairs = append(res.pairs, [2]string{k.id, cands[0]})
				used[cands[0]] = true
			default:
				sort.Strings(cands)
				res.coll[k.id] = cands
				res.collOrder = append(res.collOrder, k.id)
				for _, c := range cands {
					collided[c] = true
				}
			}
		}
	} else {
		for _, k := range pending {
			res.onlyK = append(res.onlyK, k.id)
		}
	}
	for _, h := range hs {
		if used[h.id] || collided[h.id] {
			continue
		}
		if kid, ok := rev[h.id]; ok && !firstRun && !kIDs[kid] {
			res.delK = append(res.delK, h.id)
			continue
		}
		res.onlyH = append(res.onlyH, h.id)
	}
	return res
}

// PlanAdoption inventories and matches kombify's graph and a Home Assistant
// snapshot (HOME-GRAPH-TARGET.md section 8). Floors and rooms match by mapping,
// external reference, then normalized name and alias; devices match by the
// Home Assistant device id in their external_refs. It writes nothing.
func PlanAdoption(g Graph, snap HASnapshot, m Mapping, firstRun bool) AdoptionPlan {
	m = m.Clone()
	ap := AdoptionPlan{Graph: g, Snapshot: snap, Mapping: m, FirstRun: firstRun, Leading: g.LeadingSystem}

	fName := map[string]string{}
	var fk []objK
	for _, f := range g.Floors {
		fName[f.ID] = f.Name
		fk = append(fk, objK{id: f.ID, name: f.Name, ref: haRef(f.ExternalRefs)})
	}
	hfName := map[string]string{}
	var fh []objH
	for _, f := range snap.Floors {
		hfName[f.FloorID] = f.Name
		fh = append(fh, objH{id: f.FloorID, name: f.Name, aliases: f.Aliases})
	}
	fr := matchObjects(fk, fh, m.Floors, firstRun, true)
	ap.collect("floor", fr, fName, hfName)
	floorPair := pairMap(fr.pairs)

	rName := map[string]string{}
	var rk []objK
	roomByID := map[string]Room{}
	for _, r := range g.Rooms {
		rName[r.ID] = r.Name
		roomByID[r.ID] = r
		rk = append(rk, objK{id: r.ID, name: r.Name, aliases: r.Aliases, ref: haRef(r.ExternalRefs)})
	}
	haArea := map[string]HAArea{}
	harName := map[string]string{}
	var rh []objH
	for _, a := range snap.Areas {
		haArea[a.AreaID] = a
		harName[a.AreaID] = a.Name
		rh = append(rh, objH{id: a.AreaID, name: a.Name, aliases: a.Aliases})
	}
	rr := matchObjects(rk, rh, m.Areas, firstRun, true)
	ap.collect("room", rr, rName, harName)
	roomPair := pairMap(rr.pairs)

	// Room to floor contradictions.
	for _, p := range rr.pairs {
		r, a := roomByID[p[0]], haArea[p[1]]
		kFloorHA, kFloorName := "", ""
		if r.FloorID != nil {
			kFloorHA, kFloorName = floorPair[*r.FloorID], fName[*r.FloorID]
		}
		haFloor, haFloorName := "", ""
		if a.FloorID != nil {
			haFloor, haFloorName = *a.FloorID, hfName[*a.FloorID]
		}
		if kFloorHA == haFloor && (r.FloorID == nil) == (a.FloorID == nil) {
			continue
		}
		ap.Conflicts = append(ap.Conflicts, Conflict{ID: conflictID(ConflictRoomFloorMismatch, p[0]), Kind: ConflictRoomFloorMismatch,
			Subject: "room", KombifyID: p[0], HAID: p[1], Name: r.Name, Kombify: kFloorName, HA: haFloorName})
	}

	var dk []objK
	devByID := map[string]Device{}
	dName := map[string]string{}
	for _, d := range g.Devices {
		devByID[d.ID] = d
		dName[d.ID] = d.Name
		dk = append(dk, objK{id: d.ID, name: d.Name, ref: haRef(d.ExternalRefs)})
	}
	haDev := map[string]HADevice{}
	hdName := map[string]string{}
	var dh []objH
	for _, d := range snap.Devices {
		haDev[d.ID] = d
		hdName[d.ID] = d.DisplayName()
		dh = append(dh, objH{id: d.ID, name: d.DisplayName()})
	}
	dr := matchObjects(dk, dh, m.Devices, firstRun, false)
	ap.collect("device", dr, dName, hdName)
	for _, p := range dr.pairs {
		d, hd := devByID[p[0]], haDev[p[1]]
		kRoomHA, kRoomName := "", ""
		if d.RoomID != nil {
			kRoomHA, kRoomName = roomPair[*d.RoomID], rName[*d.RoomID]
		}
		haRoom, haRoomName := "", ""
		if hd.AreaID != nil {
			haRoom, haRoomName = *hd.AreaID, harName[*hd.AreaID]
		}
		if kRoomHA == haRoom && (d.RoomID == nil) == (hd.AreaID == nil) {
			continue
		}
		ap.Conflicts = append(ap.Conflicts, Conflict{ID: conflictID(ConflictDeviceRoomMismatch, p[0]), Kind: ConflictDeviceRoomMismatch,
			Subject: "device", KombifyID: p[0], HAID: p[1], Name: d.Name, Kombify: kRoomName, HA: haRoomName})
	}
	return ap
}

func pairMap(pairs [][2]string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p[0]] = p[1]
	}
	return m
}

func conflictID(kind ConflictKind, id string) string { return string(kind) + ":" + id }

func (ap *AdoptionPlan) collect(kind string, r matchRes, kName, hName map[string]string) {
	for _, p := range r.pairs {
		ap.Matched = append(ap.Matched, Match{Kind: kind, KombifyID: p[0], HAID: p[1], Name: kName[p[0]]})
	}
	for _, id := range r.onlyK {
		ap.OnlyKombify = append(ap.OnlyKombify, Only{Kind: kind, ID: id, Name: kName[id]})
	}
	for _, id := range r.onlyH {
		ap.OnlyHA = append(ap.OnlyHA, Only{Kind: kind, ID: id, Name: hName[id]})
	}
	for _, kid := range r.collOrder {
		var names []string
		for _, h := range r.coll[kid] {
			names = append(names, hName[h])
		}
		ap.Conflicts = append(ap.Conflicts, Conflict{ID: conflictID(ConflictNameCollision, kid), Kind: ConflictNameCollision,
			Subject: kind, KombifyID: kid, Name: kName[kid], Kombify: kName[kid], HA: strings.Join(names, ", "), Candidates: r.coll[kid]})
	}
	if ap.FirstRun {
		return
	}
	for _, kid := range r.delHA {
		ap.Conflicts = append(ap.Conflicts, Conflict{ID: conflictID(ConflictDeletedOnOneSide, kind+":"+kid), Kind: ConflictDeletedOnOneSide,
			Subject: kind, KombifyID: kid, Name: kName[kid], Kombify: kName[kid], DeletedOn: SideHA})
	}
	for _, hid := range r.delK {
		ap.Conflicts = append(ap.Conflicts, Conflict{ID: conflictID(ConflictDeletedOnOneSide, kind+":"+hid), Kind: ConflictDeletedOnOneSide,
			Subject: kind, HAID: hid, Name: hName[hid], HA: hName[hid], DeletedOn: SideKombify})
	}
}

// Resolve applies the person's decisions (conflict id to side) and returns the
// merged graph together with the projection that writes the result to Home
// Assistant. A conflict without a decision follows plan.Leading.
//
// Semantics: matched objects are linked in the mapping and get a Home
// Assistant external_ref; objects only in Home Assistant are imported into the
// graph (devices as external, class other, role control); objects only in
// kombify are projected. For room/device placement conflicts the chosen side's
// value wins. A name_collision resolved for Home Assistant links the kombify
// object to the first candidate; resolved for kombify it leaves both unlinked
// and the projection reports the collision as drift. A deletion in Home
// Assistant resolved for Home Assistant removes the kombify object, resolved
// for kombify it is recreated; a deletion in kombify resolved for Home
// Assistant re-imports the object, resolved for kombify leaves the Home
// Assistant object alone (the plan never deletes there). When Home Assistant
// leads, a matched object's name is taken from Home Assistant; when kombify
// leads, a differing Home Assistant name is renamed by the projection.
//
// The merged graph is validated; a validation error is returned together with
// the graph and an empty plan, and nothing should be written.
func Resolve(plan AdoptionPlan, decisions map[string]Side) (Graph, Plan, error) {
	r := &resolver{ap: plan, m: plan.Mapping.Clone(), g: copyGraph(plan.Graph)}
	r.dec = func(id string) Side {
		if s, ok := decisions[id]; ok && (s == SideKombify || s == SideHA) {
			return s
		}
		if plan.Leading == LeadingHomeAssistant {
			return SideHA
		}
		return SideKombify
	}
	r.run()
	r.g.LeadingSystem = plan.Leading
	if r.g.LeadingSystem != LeadingKombify && r.g.LeadingSystem != LeadingHomeAssistant {
		r.g.LeadingSystem = LeadingKombify
	}
	if err := r.g.Validate(); err != nil {
		return r.g, Plan{Mapping: r.m}, fmt.Errorf("homegraph: merged graph is invalid: %w", err)
	}
	// Confirmation authorizes the resolved values even when HA changed them
	// since the previous sync. Ordinary drift protection must not override it.
	r.m.Synced = map[string]string{}
	return r.g, projectOnto(r.g, plan.Snapshot, r.m, nil), nil
}

type resolver struct {
	ap  AdoptionPlan
	m   Mapping
	g   Graph
	dec func(string) Side

	haFloor map[string]HAFloor
	haArea  map[string]HAArea
	haDev   map[string]HADevice
}

func copyGraph(g Graph) Graph {
	c := g
	c.MirrorClasses = append([]string{}, g.MirrorClasses...)
	c.Floors = append([]Floor{}, g.Floors...)
	c.Rooms = append([]Room{}, g.Rooms...)
	c.Devices = append([]Device{}, g.Devices...)
	c.Assignments = append([]Assignment{}, g.Assignments...)
	return c
}

func (r *resolver) link(kind, kid, hid string) {
	switch kind {
	case "floor":
		r.m.Floors[kid] = hid
		for i := range r.g.Floors {
			if r.g.Floors[i].ID == kid {
				r.g.Floors[i].ExternalRefs = withRef(r.g.Floors[i].ExternalRefs, SystemHomeAssistant, hid)
				if r.leadHA() {
					if f, ok := r.haFloor[hid]; ok && f.Name != "" {
						r.g.Floors[i].Name = clipName(f.Name)
						if f.Level != nil {
							r.g.Floors[i].Level = *f.Level
						}
					}
				}
			}
		}
	case "room":
		r.m.Areas[kid] = hid
		for i := range r.g.Rooms {
			if r.g.Rooms[i].ID == kid {
				r.g.Rooms[i].ExternalRefs = withRef(r.g.Rooms[i].ExternalRefs, SystemHomeAssistant, hid)
				if r.leadHA() {
					if a, ok := r.haArea[hid]; ok && a.Name != "" {
						r.g.Rooms[i].Name = clipName(a.Name)
					}
				}
			}
		}
	case "device":
		r.m.Devices[kid] = hid
		for i := range r.g.Devices {
			if r.g.Devices[i].ID == kid {
				r.g.Devices[i].ExternalRefs = withRef(r.g.Devices[i].ExternalRefs, SystemHomeAssistant, hid)
				if r.leadHA() {
					if d, ok := r.haDev[hid]; ok {
						r.g.Devices[i].Name = clipName(d.DisplayName())
					}
				}
			}
		}
	}
}

func (r *resolver) leadHA() bool { return r.ap.Leading == LeadingHomeAssistant }

func (r *resolver) run() {
	r.haFloor, r.haArea, r.haDev = map[string]HAFloor{}, map[string]HAArea{}, map[string]HADevice{}
	for _, f := range r.ap.Snapshot.Floors {
		r.haFloor[f.FloorID] = f
	}
	for _, a := range r.ap.Snapshot.Areas {
		r.haArea[a.AreaID] = a
	}
	for _, d := range r.ap.Snapshot.Devices {
		r.haDev[d.ID] = d
	}

	// 1. Links of matched objects.
	for _, mt := range r.ap.Matched {
		r.link(mt.Kind, mt.KombifyID, mt.HAID)
	}

	reimport := map[string]bool{} // HA ids to import again (deleted in kombify, decided for HA)
	for _, c := range r.ap.Conflicts {
		side := r.dec(c.ID)
		switch c.Kind {
		case ConflictNameCollision:
			if side == SideHA && len(c.Candidates) > 0 {
				r.link(c.Subject, c.KombifyID, c.Candidates[0])
			}
		case ConflictDeletedOnOneSide:
			r.deleted(c, side, reimport)
		}
	}

	// 2. Imports of Home Assistant objects kombify does not know.
	r.importHA(reimport)

	// 3. Placement conflicts.
	for _, c := range r.ap.Conflicts {
		side := r.dec(c.ID)
		switch c.Kind {
		case ConflictRoomFloorMismatch:
			if side == SideKombify {
				delete(r.m.Synced, "area:"+c.KombifyID+":floor")
				continue
			}
			var fid *string
			if a := r.haArea[c.HAID]; a.FloorID != nil {
				if k := reverse(r.m.Floors, *a.FloorID); k != "" {
					fid = &k
				}
			}
			for i := range r.g.Rooms {
				if r.g.Rooms[i].ID == c.KombifyID {
					r.g.Rooms[i].FloorID = fid
				}
			}
			r.m.Synced["area:"+c.KombifyID+":floor"] = deref(r.haArea[c.HAID].FloorID)
		case ConflictDeviceRoomMismatch:
			if side == SideKombify {
				delete(r.m.Synced, "device:"+c.KombifyID+":area")
				continue
			}
			var rid *string
			if d := r.haDev[c.HAID]; d.AreaID != nil {
				if k := reverse(r.m.Areas, *d.AreaID); k != "" {
					rid = &k
				}
			}
			for i := range r.g.Devices {
				if r.g.Devices[i].ID == c.KombifyID {
					r.g.Devices[i].RoomID = rid
				}
			}
			r.m.Synced["device:"+c.KombifyID+":area"] = deref(r.haDev[c.HAID].AreaID)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func reverse(m map[string]string, hid string) string {
	for k, v := range m {
		if v == hid {
			return k
		}
	}
	return ""
}

func (r *resolver) deleted(c Conflict, side Side, reimport map[string]bool) {
	if c.DeletedOn == SideHA {
		// kombify has it, Home Assistant lost it.
		switch c.Subject {
		case "floor":
			delete(r.m.Floors, c.KombifyID)
		case "room":
			delete(r.m.Areas, c.KombifyID)
		case "device":
			delete(r.m.Devices, c.KombifyID)
		}
		for _, k := range []string{":name", ":level", ":floor", ":area"} {
			delete(r.m.Synced, c.Subject2()+c.KombifyID+k)
		}
		if side == SideHA {
			r.remove(c.Subject, c.KombifyID)
		}
		return
	}
	// Home Assistant has it, kombify lost it.
	switch c.Subject {
	case "floor":
		delete(r.m.Floors, reverse(r.m.Floors, c.HAID))
	case "room":
		delete(r.m.Areas, reverse(r.m.Areas, c.HAID))
	case "device":
		delete(r.m.Devices, reverse(r.m.Devices, c.HAID))
	}
	if side == SideHA {
		reimport[c.Subject+":"+c.HAID] = true
	}
}

// Subject2 is the key prefix the planner uses for the subject's synced state.
func (c Conflict) Subject2() string {
	if c.Subject == "room" {
		return "area:"
	}
	return c.Subject + ":"
}

// remove deletes a kombify object from the merged graph and detaches what
// referenced it.
func (r *resolver) remove(kind, id string) {
	switch kind {
	case "floor":
		out := r.g.Floors[:0:0]
		for _, f := range r.g.Floors {
			if f.ID != id {
				out = append(out, f)
			}
		}
		r.g.Floors = out
		for i := range r.g.Rooms {
			if r.g.Rooms[i].FloorID != nil && *r.g.Rooms[i].FloorID == id {
				r.g.Rooms[i].FloorID = nil
			}
		}
	case "room":
		out := r.g.Rooms[:0:0]
		for _, x := range r.g.Rooms {
			if x.ID != id {
				out = append(out, x)
			}
		}
		r.g.Rooms = out
		for i := range r.g.Devices {
			if r.g.Devices[i].RoomID != nil && *r.g.Devices[i].RoomID == id {
				r.g.Devices[i].RoomID = nil
			}
		}
	case "device":
		// HA can remove its link, never the kombify identity or assignments.
		for i := range r.g.Devices {
			d := &r.g.Devices[i]
			if d.ID == id && d.Identity.Kind != IdentityExt {
				refs := []ExternalRef{}
				for _, ref := range d.ExternalRefs {
					if ref.System != SystemHomeAssistant {
						refs = append(refs, ref)
					}
				}
				d.ExternalRefs = refs
				return
			}
		}
		out := r.g.Devices[:0:0]
		for _, x := range r.g.Devices {
			if x.ID != id {
				out = append(out, x)
			}
		}
		r.g.Devices = out
		as := r.g.Assignments[:0:0]
		for _, a := range r.g.Assignments {
			if a.DeviceID != id && a.TargetDeviceID != id {
				as = append(as, a)
			}
		}
		r.g.Assignments = as
	}
}

func importedHAID(prefix, space, id string) string {
	sum := sha256.Sum256([]byte(space + ":" + prefix + id))
	return prefix + hex.EncodeToString(sum[:12])
}

func (r *resolver) importHA(reimport map[string]bool) {
	want := func(kind, hid string) bool {
		if reimport[kind+":"+hid] {
			return true
		}
		for _, o := range r.ap.OnlyHA {
			if o.Kind == kind && o.ID == hid {
				return true
			}
		}
		return false
	}
	for _, f := range r.ap.Snapshot.Floors {
		if !want("floor", f.FloorID) || f.Name == "" {
			continue
		}
		id := importedHAID("flr_", r.g.SpaceID, f.FloorID)
		level := 0
		if f.Level != nil && *f.Level >= -10 && *f.Level <= 100 {
			level = *f.Level
		}
		r.g.Floors = append(r.g.Floors, Floor{ID: id, Name: clipName(f.Name), Level: level,
			ExternalRefs: []ExternalRef{{System: SystemHomeAssistant, Ref: f.FloorID}}})
		r.m.Floors[id] = f.FloorID
	}
	for _, a := range r.ap.Snapshot.Areas {
		if !want("room", a.AreaID) || a.Name == "" {
			continue
		}
		id := importedHAID("rom_", r.g.SpaceID, a.AreaID)
		var fid *string
		if a.FloorID != nil {
			if k := reverse(r.m.Floors, *a.FloorID); k != "" {
				fid = &k
			}
		}
		aliases := []string{}
		seen := map[string]bool{}
		for _, al := range a.Aliases {
			al = clipName(al)
			if al != "" && !seen[al] && len(aliases) < 8 {
				seen[al] = true
				aliases = append(aliases, al)
			}
		}
		r.g.Rooms = append(r.g.Rooms, Room{ID: id, Name: clipName(a.Name), FloorID: fid, Aliases: aliases,
			ExternalRefs: []ExternalRef{{System: SystemHomeAssistant, Ref: a.AreaID}}})
		r.m.Areas[id] = a.AreaID
	}
	for _, d := range r.ap.Snapshot.Devices {
		if !want("device", d.ID) || !reExtRef.MatchString(d.ID) || !reIdentRef.MatchString(d.ID) {
			continue
		}
		name := clipName(d.DisplayName())
		if utf8.RuneCountInString(name) == 0 {
			continue
		}
		id := importedHAID("hdv_", r.g.SpaceID, d.ID)
		var rid *string
		if d.AreaID != nil {
			if k := reverse(r.m.Areas, *d.AreaID); k != "" {
				rid = &k
			}
		}
		r.g.Devices = append(r.g.Devices, Device{ID: id, Name: name, Class: ClassOther, Roles: []string{RoleControl}, RoomID: rid,
			Identity:     Identity{Kind: IdentityExt, Ref: d.ID},
			ExternalRefs: []ExternalRef{{System: SystemHomeAssistant, Ref: d.ID}}, Status: StatusActive})
		r.m.Devices[id] = d.ID
	}
}
