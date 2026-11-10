package homegraph

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// LabelName is the Home Assistant label kombify puts on the areas it creates.
const LabelName = "kombify"

// OpKind names one write against Home Assistant. There is no delete kind:
// kombify removes nothing in Home Assistant on its own.
type OpKind string

// Operation kinds, in the order a plan lists them.
const (
	OpEnsureLabel   OpKind = "ensure_label"
	OpCreateFloor   OpKind = "create_floor"
	OpUpdateFloor   OpKind = "update_floor"
	OpCreateArea    OpKind = "create_area"
	OpUpdateArea    OpKind = "update_area"
	OpSetDeviceArea OpKind = "set_device_area"
)

// Op is one planned write. Home Assistant ids are resolved from the mapping at
// apply time, so an op may reference an object an earlier op creates.
type Op struct {
	Kind      OpKind
	KombifyID string
	// HAID is the Home Assistant object the op changes, empty for creates.
	HAID string
	Name string
	// Level is the floor level of a floor op.
	Level int
	// ParentKombifyID is the floor of an area op ("" for none) or the room of
	// a device op.
	ParentKombifyID string
	// Fields lists the changed fields of an update op: name, level, floor.
	Fields []string
}

// Drift is a difference between the graph and a mapped Home Assistant object
// that the plan does not overwrite: Home Assistant changed since the last
// sync, the object vanished, or a name collides with an unmapped object.
type Drift struct {
	Kind      string // floor, area, device
	Field     string // name, level, floor, area, deleted, name_collision
	KombifyID string
	HAID      string
	Graph     string
	HA        string
}

// Plan is the set of writes that projects the graph onto Home Assistant.
type Plan struct {
	Ops   []Op
	Drift []Drift
	// Mapping is the mapping the plan starts from, with links found while
	// planning. ApplyPlan starts from it.
	Mapping Mapping
}

// Empty reports whether the plan writes nothing.
func (p Plan) Empty() bool { return len(p.Ops) == 0 }

// Restore names one drifted field whose kombify value overwrites Home Assistant
// in this run instead of being reported as drift. Kind is floor, area or
// device; Field is name, level, floor or area. It never creates or deletes.
type Restore struct {
	Kind      string `json:"kind"`
	KombifyID string `json:"kombifyId"`
	Field     string `json:"field"`
}

func (r Restore) key() string { return r.Kind + ":" + r.KombifyID + ":" + r.Field }

type action int

const (
	actNone action = iota
	actUpdate
	actDrift
)

type planner struct {
	g    Graph
	snap HASnapshot
	m    Mapping
	plan Plan
	// restore holds the field keys ("floor:<id>:name") the owner chose to
	// overwrite in Home Assistant.
	restore map[string]bool
	floor   map[string]HAFloor
	area    map[string]HAArea
	dev     map[string]HADevice
	// skipF and skipA hold the kombify ids of floors and rooms the planner
	// could not project (name collision, vanished in Home Assistant, or itself
	// dependent on such an object); their dependents are not planned either.
	skipF map[string]bool
	skipA map[string]bool
}

func newPlanner(g Graph, snap HASnapshot, m Mapping, restore []Restore) *planner {
	m = m.Clone()
	p := &planner{g: g, snap: snap, m: m, restore: map[string]bool{},
		floor: map[string]HAFloor{}, area: map[string]HAArea{}, dev: map[string]HADevice{},
		skipF: map[string]bool{}, skipA: map[string]bool{}}
	for _, r := range restore {
		p.restore[r.key()] = true
	}
	for _, f := range snap.Floors {
		p.floor[f.FloorID] = f
	}
	for _, a := range snap.Areas {
		p.area[a.AreaID] = a
	}
	for _, d := range snap.Devices {
		p.dev[d.ID] = d
	}
	return p
}

// decide compares one field. Equal values record the sync state. A differing
// value that equals the last synced one is a kombify change (update); a value
// that changed in Home Assistant since the last sync is drift.
func (p *planner) decide(key, graphVal, haVal string) action {
	if graphVal == haVal {
		p.m.Synced[key] = graphVal
		return actNone
	}
	if last, ok := p.m.Synced[key]; ok && last != haVal && !p.restore[key] {
		return actDrift
	}
	return actUpdate
}

// PlanProjection plans the writes that project g onto Home Assistant when
// kombify leads. When Home Assistant leads, nothing is written from here and
// the plan is empty. It never plans a delete.
func PlanProjection(g Graph, snap HASnapshot, m Mapping) Plan {
	return PlanProjectionRestore(g, snap, m, nil)
}

// PlanProjectionRestore is PlanProjection where the fields in restore are
// overwritten in Home Assistant with the kombify value even when Home
// Assistant changed them since the last sync. Every other drift stays
// untouched and is reported.
func PlanProjectionRestore(g Graph, snap HASnapshot, m Mapping, restore []Restore) Plan {
	if g.LeadingSystem != LeadingKombify {
		return Plan{Mapping: m.Clone()}
	}
	return projectOnto(g, snap, m, restore)
}

// projectOnto plans the projection regardless of the leading system.
func projectOnto(g Graph, snap HASnapshot, m Mapping, restore []Restore) Plan {
	p := newPlanner(g, snap, m, restore)
	p.label()
	p.floors()
	p.areas()
	p.devices()
	p.plan.Mapping = p.m
	return p.plan
}

func (p *planner) label() {
	if _, ok := p.labelByID(p.m.LabelID); ok {
		return
	}
	for _, l := range p.snap.Labels {
		if normName(l.Name) == LabelName {
			p.m.LabelID = l.LabelID
			return
		}
	}
	p.m.LabelID = ""
	p.plan.Ops = append(p.plan.Ops, Op{Kind: OpEnsureLabel, Name: LabelName})
}

func (p *planner) labelByID(id string) (HALabel, bool) {
	for _, l := range p.snap.Labels {
		if id != "" && l.LabelID == id {
			return l, true
		}
	}
	return HALabel{}, false
}

func (p *planner) floors() {
	for _, f := range p.g.Floors {
		haID := p.m.Floors[f.ID]
		if haID == "" {
			if ref := haRef(f.ExternalRefs); ref != "" {
				if _, ok := p.floor[ref]; ok {
					haID = ref
					p.m.Floors[f.ID] = ref
				}
			}
		}
		if haID == "" {
			if c, ok := p.floorByName(f.Name); ok {
				p.drift("floor", "name_collision", f.ID, c.FloorID, f.Name, c.Name)
				p.skipF[f.ID] = true
				continue
			}
			p.plan.Ops = append(p.plan.Ops, Op{Kind: OpCreateFloor, KombifyID: f.ID, Name: f.Name, Level: f.Level})
			continue
		}
		cur, ok := p.floor[haID]
		if !ok {
			p.drift("floor", "deleted", f.ID, haID, f.Name, "")
			p.skipF[f.ID] = true
			continue
		}
		var fields []string
		switch p.decide("floor:"+f.ID+":name", f.Name, cur.Name) {
		case actUpdate:
			fields = append(fields, "name")
		case actDrift:
			p.drift("floor", "name", f.ID, haID, f.Name, cur.Name)
		}
		haLevel := ""
		if cur.Level != nil {
			haLevel = strconv.Itoa(*cur.Level)
		}
		switch p.decide("floor:"+f.ID+":level", strconv.Itoa(f.Level), haLevel) {
		case actUpdate:
			fields = append(fields, "level")
		case actDrift:
			p.drift("floor", "level", f.ID, haID, strconv.Itoa(f.Level), haLevel)
		}
		if len(fields) > 0 {
			p.plan.Ops = append(p.plan.Ops, Op{Kind: OpUpdateFloor, KombifyID: f.ID, HAID: haID, Name: f.Name, Level: f.Level, Fields: fields})
		}
	}
}

// floorTarget is the Home Assistant floor id a room belongs to, "" for none.
// A floor that does not exist in Home Assistant yet yields a placeholder that
// never equals a real id.
func (p *planner) floorTarget(floorID *string) string {
	if floorID == nil {
		return ""
	}
	if id := p.m.Floors[*floorID]; id != "" {
		return id
	}
	return "kombify:" + *floorID
}

func (p *planner) areas() {
	for _, r := range p.g.Rooms {
		haID := p.m.Areas[r.ID]
		if haID == "" {
			if ref := haRef(r.ExternalRefs); ref != "" {
				if _, ok := p.area[ref]; ok {
					haID = ref
					p.m.Areas[r.ID] = ref
				}
			}
		}
		parent := ""
		if r.FloorID != nil {
			parent = *r.FloorID
		}
		parentSkipped := r.FloorID != nil && p.skipF[*r.FloorID]
		if haID == "" {
			if c, ok := p.areaByName(r.Name); ok {
				p.drift("area", "name_collision", r.ID, c.AreaID, r.Name, c.Name)
				p.skipA[r.ID] = true
				continue
			}
			if parentSkipped {
				p.drift("area", "parent_skipped", r.ID, "", r.Name, *r.FloorID)
				p.skipA[r.ID] = true
				continue
			}
			p.plan.Ops = append(p.plan.Ops, Op{Kind: OpCreateArea, KombifyID: r.ID, Name: r.Name, ParentKombifyID: parent})
			continue
		}
		cur, ok := p.area[haID]
		if !ok {
			p.drift("area", "deleted", r.ID, haID, r.Name, "")
			p.skipA[r.ID] = true
			continue
		}
		var fields []string
		switch p.decide("area:"+r.ID+":name", r.Name, cur.Name) {
		case actUpdate:
			fields = append(fields, "name")
		case actDrift:
			p.drift("area", "name", r.ID, haID, r.Name, cur.Name)
		}
		haFloor := ""
		if cur.FloorID != nil {
			haFloor = *cur.FloorID
		}
		switch p.decide("area:"+r.ID+":floor", p.floorTarget(r.FloorID), haFloor) {
		case actUpdate:
			if parentSkipped {
				p.drift("area", "parent_skipped", r.ID, haID, r.Name, *r.FloorID)
				break
			}
			fields = append(fields, "floor")
		case actDrift:
			p.drift("area", "floor", r.ID, haID, p.floorTarget(r.FloorID), haFloor)
		}
		if len(fields) > 0 {
			p.plan.Ops = append(p.plan.Ops, Op{Kind: OpUpdateArea, KombifyID: r.ID, HAID: haID, Name: r.Name, ParentKombifyID: parent, Fields: fields})
		}
	}
}

func (p *planner) devices() {
	rooms := map[string]Room{}
	for _, r := range p.g.Rooms {
		rooms[r.ID] = r
	}
	for _, d := range p.g.Devices {
		ref := haRef(d.ExternalRefs)
		if ref == "" || d.Status != StatusActive || d.RoomID == nil {
			continue
		}
		cur, ok := p.dev[ref]
		if !ok {
			continue // a kombify device Home Assistant does not know: adoption handles it
		}
		p.m.Devices[d.ID] = ref
		if p.skipA[*d.RoomID] {
			p.drift("device", "parent_skipped", d.ID, ref, *d.RoomID, "")
			continue
		}
		target := p.m.Areas[*d.RoomID]
		if target == "" {
			target = "kombify:" + *d.RoomID
		}
		haArea := ""
		if cur.AreaID != nil {
			haArea = *cur.AreaID
		}
		switch p.decide("device:"+d.ID+":area", target, haArea) {
		case actUpdate:
			p.plan.Ops = append(p.plan.Ops, Op{Kind: OpSetDeviceArea, KombifyID: d.ID, HAID: ref, ParentKombifyID: *d.RoomID, Fields: []string{"area"}})
		case actDrift:
			p.drift("device", "area", d.ID, ref, target, haArea)
		}
	}
}

func (p *planner) drift(kind, field, kid, haID, graph, ha string) {
	p.plan.Drift = append(p.plan.Drift, Drift{Kind: kind, Field: field, KombifyID: kid, HAID: haID, Graph: graph, HA: ha})
}

func (p *planner) floorByName(name string) (HAFloor, bool) {
	n := normName(name)
	for _, f := range p.snap.Floors {
		if normName(f.Name) == n {
			return f, true
		}
	}
	return HAFloor{}, false
}

func (p *planner) areaByName(name string) (HAArea, bool) {
	n := normName(name)
	for _, a := range p.snap.Areas {
		if normName(a.Name) == n {
			return a, true
		}
	}
	return HAArea{}, false
}

// normName normalizes a name for matching: lower case, punctuation and
// separators folded to single spaces. A name without letters or digits falls
// back to its trimmed lower-case form so symbol-only names stay distinct.
func normName(s string) string {
	if n := normLetters(s); n != "" {
		return n
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func normLetters(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		} else {
			space = true
		}
	}
	return b.String()
}

// ApplyPlan executes the ops against Home Assistant and returns the updated
// mapping. It is idempotent: an object the mapping already links to an
// existing Home Assistant object is not created again, and an update repeats
// harmlessly. On an error it returns the mapping as far as it got, so the
// caller persists it and the next run resumes. An op whose parent floor, area
// or room is not in Home Assistant (it was skipped or its creation failed
// earlier) is not executed; it is returned in skipped as drift with the field
// "parent_skipped" and never aborts the remaining ops.
func ApplyPlan(ctx context.Context, c Registry, plan Plan) (m Mapping, skipped []Drift, err error) {
	m = plan.Mapping.Clone()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return m, nil, err
	}
	floors := map[string]bool{}
	for _, f := range snap.Floors {
		floors[f.FloorID] = true
	}
	areas := map[string]bool{}
	for _, a := range snap.Areas {
		areas[a.AreaID] = true
	}
	for _, op := range plan.Ops {
		if err := ctx.Err(); err != nil {
			return m, skipped, err
		}
		if err := applyOp(ctx, c, &m, op, snap, floors, areas, &skipped); err != nil {
			return m, skipped, fmt.Errorf("homegraph: %s %s: %w", op.Kind, op.KombifyID, err)
		}
	}
	return m, skipped, nil
}

func applyOp(ctx context.Context, c Registry, m *Mapping, op Op, snap HASnapshot, floors, areas map[string]bool, skipped *[]Drift) error {
	skip := func(kind, field string, op Op, parent string) {
		*skipped = append(*skipped, Drift{Kind: kind, Field: field, KombifyID: op.KombifyID, HAID: op.HAID, Graph: op.Name, HA: parent})
	}
	switch op.Kind {
	case OpEnsureLabel:
		for _, l := range snap.Labels {
			if normName(l.Name) == LabelName {
				m.LabelID = l.LabelID
				return nil
			}
		}
		l, err := c.CreateLabel(ctx, op.Name)
		if err != nil {
			return err
		}
		m.LabelID = l.LabelID
	case OpCreateFloor:
		if id := m.Floors[op.KombifyID]; id != "" && floors[id] {
			return nil
		}
		f, err := c.CreateFloor(ctx, op.Name, op.Level)
		if err != nil {
			return err
		}
		m.Floors[op.KombifyID] = f.FloorID
		floors[f.FloorID] = true
		m.Created["floor:"+f.FloorID] = true
		m.Synced["floor:"+op.KombifyID+":name"] = op.Name
		m.Synced["floor:"+op.KombifyID+":level"] = strconv.Itoa(op.Level)
	case OpUpdateFloor:
		var name *string
		var level *int
		for _, f := range op.Fields {
			switch f {
			case "name":
				n := op.Name
				name = &n
			case "level":
				l := op.Level
				level = &l
			}
		}
		if err := c.UpdateFloor(ctx, op.HAID, name, level); err != nil {
			return err
		}
		if name != nil {
			m.Synced["floor:"+op.KombifyID+":name"] = op.Name
		}
		if level != nil {
			m.Synced["floor:"+op.KombifyID+":level"] = strconv.Itoa(op.Level)
		}
	case OpCreateArea:
		if id := m.Areas[op.KombifyID]; id != "" && areas[id] {
			return nil
		}
		var floorID *string
		if op.ParentKombifyID != "" {
			id := m.Floors[op.ParentKombifyID]
			if id == "" || !floors[id] {
				skip("area", "parent_skipped", op, op.ParentKombifyID)
				return nil
			}
			floorID = &id
		}
		var labels []string
		if m.LabelID != "" {
			labels = []string{m.LabelID}
		}
		a, err := c.CreateArea(ctx, op.Name, floorID, labels)
		if err != nil {
			return err
		}
		m.Areas[op.KombifyID] = a.AreaID
		areas[a.AreaID] = true
		m.Created["area:"+a.AreaID] = true
		m.Synced["area:"+op.KombifyID+":name"] = op.Name
		m.Synced["area:"+op.KombifyID+":floor"] = floorTargetValue(floorID)
	case OpUpdateArea:
		var u AreaUpdate
		floorVal := ""
		for _, f := range op.Fields {
			switch f {
			case "name":
				n := op.Name
				u.Name = &n
			case "floor":
				if op.ParentKombifyID == "" {
					u.ClearFloor = true
				} else {
					id := m.Floors[op.ParentKombifyID]
					if id == "" || !floors[id] {
						skip("area", "parent_skipped", op, op.ParentKombifyID)
						continue
					}
					u.FloorID = &id
					floorVal = id
				}
			}
		}
		if u.Name == nil && u.FloorID == nil && !u.ClearFloor {
			return nil
		}
		if err := c.UpdateArea(ctx, op.HAID, u); err != nil {
			return err
		}
		if u.Name != nil {
			m.Synced["area:"+op.KombifyID+":name"] = op.Name
		}
		if u.FloorID != nil || u.ClearFloor {
			m.Synced["area:"+op.KombifyID+":floor"] = floorVal
		}
	case OpSetDeviceArea:
		id := m.Areas[op.ParentKombifyID]
		if id == "" || !areas[id] {
			skip("device", "parent_skipped", op, op.ParentKombifyID)
			return nil
		}
		if err := c.SetDeviceArea(ctx, op.HAID, id); err != nil {
			return err
		}
		m.Devices[op.KombifyID] = op.HAID
		m.Synced["device:"+op.KombifyID+":area"] = id
	default:
		return fmt.Errorf("unknown op %q", op.Kind)
	}
	return nil
}

func floorTargetValue(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}
