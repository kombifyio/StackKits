package backuplifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
)

// AppGroupControl is v2 inside the EXISTING signed owning journal. Original
// remains the immutable G0 anchor; generations are appended, never substituted
// into the v1 exact-container field. A native acknowledgment is not a receipt.
type AppGroupControl struct {
	Version  int                `json:"version"`
	Binding  AppGroupBinding    `json:"binding"`
	Original AppGroupGeneration `json:"original"`
	Effects  []AppGroupEffect   `json:"effects,omitempty"`
	Closed   bool               `json:"closed,omitempty"`
}
type AppGroupBinding struct {
	ManagerID                string           `json:"managerId"`
	ManagerContainerID       string           `json:"managerContainerId"`
	ManagerImage             string           `json:"managerImage"`
	SourceDigest             string           `json:"sourceDigest"`
	InstallationDigest       string           `json:"installationDigest"`
	OwnerRef                 string           `json:"ownerRef"`
	SiteRef                  string           `json:"siteRef"`
	NodeRef                  string           `json:"nodeRef"`
	AppID                    int              `json:"appId"`
	AppURN                   string           `json:"appUrn"`
	LogicalProject           string           `json:"logicalProject"`
	PhysicalProject          string           `json:"physicalProject"`
	BindingDigest            string           `json:"bindingDigest"`
	JournalOperationID       string           `json:"journalOperationId"`
	NativeJournalOperationID string           `json:"nativeJournalOperationId"`
	PolicySourceDigest       string           `json:"policySourceDigest"`
	AuthorityLineage         AuthorityLineage `json:"authorityLineage"`
	QualificationDigest      string           `json:"qualificationDigest"`
	AppliedRequestDigest     string           `json:"appliedRequestDigest"`
	CustodyDigest            string           `json:"custodyDigest"`
	GroupID                  string           `json:"groupId"`
	RecipeDigest             string           `json:"recipeDigest"`
	EffectiveInputDigest     string           `json:"effectiveInputDigest"`
}
type AppGroupVolume struct {
	Name           string `json:"name"`
	Mountpoint     string `json:"mountpoint"`
	Driver         string `json:"driver"`
	IdentityDigest string `json:"identityDigest"`
}
type AppGroupGeneration struct {
	ConfigDigests map[string]string           `json:"configDigests"`
	Containers    []SnapshotQuiescedContainer `json:"containers"`
	Volumes       []AppGroupVolume            `json:"volumes"`
	Absent        bool                        `json:"absent"`
	Partial       bool                        `json:"partial,omitempty"`
}
type AppGroupEffect struct {
	Action     string              `json:"action"`
	State      string              `json:"state"`
	Handle     string              `json:"handle,omitempty"`
	Receipt    *AppGroupReceipt    `json:"receipt,omitempty"`
	Negative   *AppGroupNegative   `json:"negative,omitempty"`
	Generation *AppGroupGeneration `json:"generation,omitempty"`
}
type AppGroupReceipt struct {
	OperationID            string `json:"operationId"`
	PredecessorOperationID string `json:"predecessorOperationId,omitempty"`
	RequestDigest          string `json:"requestDigest"`
	WitnessDigest          string `json:"witnessDigest"`
	ComposeExit            int    `json:"composeExit"`
	PreviousCustodyDigest  string `json:"previousCustodyDigest"`
}

const ControlAdopted = "adopted"
const ControlNegativeAdopted = "negative-adopted"
const ControlFailed = "failed"

var ErrAppGroupFailed = errors.New("app-group original effect settled negatively")

// A negative witness never proves STOP or START success. Local undispatched
// proof is bound to the exact signed pre-dispatch journal; native proof is
// bound to the original operation and its authenticated durable witness.
type AppGroupNegative struct {
	DispatchPhase          string `json:"dispatchPhase,omitempty"`
	OperationID            string `json:"operationId,omitempty"`
	PredecessorOperationID string `json:"predecessorOperationId,omitempty"`
	RequestDigest          string `json:"requestDigest,omitempty"`
	WitnessDigest          string `json:"witnessDigest"`
	Kind                   string `json:"kind"`
	Phase                  string `json:"phase"`
	Reason                 string `json:"reason"`
	NoNativeActuation      bool   `json:"noNativeActuation"`
	ComposeExit            *int   `json:"composeExit,omitempty"`
	PreviousCustodyDigest  string `json:"previousCustodyDigest"`
}
type AppGroupNegativeDriver interface {
	RecoverUndispatched(context.Context, AppGroupControl, AppGroupEffect) (AppGroupNegative, AppGroupGeneration, error)
	ReadbackNegative(context.Context, AppGroupControl, string, string) (AppGroupNegative, AppGroupGeneration, error)
}

func groupEffectSettled(e AppGroupEffect) bool {
	return e.State == ControlObserved || e.State == ControlFailed
}
func groupActionAllowed(g AppGroupControl, action string) bool {
	if len(g.Effects) == 0 {
		return action == "stop"
	}
	if len(g.Effects) >= 16 {
		return false
	}
	e := g.Effects[len(g.Effects)-1]
	if !groupEffectSettled(e) {
		return false
	}
	if e.State == ControlFailed {
		return action == "rollback-stop" || action == "rollback-start" && AppGroupStopped(g)
	}
	switch e.Action {
	case "stop":
		return action == "start" || action == "rollback-start"
	case "start":
		return action == "rollback-stop"
	case "rollback-stop":
		return action == "rollback-start"
	}
	return false
}

// The driver must bind its dormant handle to an exact command/action/nonce and
// native operation. Complete proves terminal native success; Readback then
// independently checks absent writers or the full successor graph and volumes.
// Adopt must atomically CAS existing node custody from the preceding journaled
// generation to the already signed successor. It must be idempotent on replay.
type AppGroupControlDriver interface {
	RuntimeControlDriver
	Hold(context.Context, AppGroupControl) error
	VerifyCurrent(context.Context, AppGroupControl) error
	Readback(context.Context, AppGroupControl, string, string) (AppGroupReceipt, AppGroupGeneration, error)
	Adopt(context.Context, AppGroupControl) error
	Close(context.Context, AppGroupControl) error
}

var groupVolumeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,254}$`)
var groupProjectName = regexp.MustCompile(`^stackkit-[a-z0-9][a-z0-9-]{0,125}$`)
var groupPinnedImage = regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`)
var groupSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var groupID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func CloneAppGroups(groups []AppGroupControl) []AppGroupControl {
	if groups == nil {
		return nil
	}
	raw, _ := json.Marshal(groups)
	var out []AppGroupControl
	_ = json.Unmarshal(raw, &out)
	return out
}
func AppGroupDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (g AppGroupControl) InitialRunning() bool {
	return len(g.Original.Containers) > 0 && g.Original.Containers[0].WasRunning
}
func (g AppGroupControl) Current() AppGroupGeneration {
	for i := len(g.Effects) - 1; i >= 0; i-- {
		if g.Effects[i].Generation != nil {
			return *g.Effects[i].Generation
		}
	}
	return g.Original
}
func ValidateAppGroups(groups []AppGroupControl) error {
	seen := map[string]bool{}
	for _, g := range groups {
		b := g.Binding
		if !groupSHA.MatchString(b.CustodyDigest) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(b.AppliedRequestDigest) {
			return errors.New("group lacks exact applied request/custody preimage")
		}
		if g.Version != 2 || !groupProjectName.MatchString(b.LogicalProject) || !groupID.MatchString(b.ManagerID) || !groupID.MatchString(b.GroupID) || !ValidateSnapshotOperationID(b.JournalOperationID) || !groupID.MatchString(b.NativeJournalOperationID) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(b.PolicySourceDigest) || !groupSHA.MatchString(b.ManagerContainerID) || !groupPinnedImage.MatchString(b.ManagerImage) || b.OwnerRef == "" || b.SiteRef == "" || b.NodeRef == "" || b.AppID < 1 || b.PhysicalProject != b.LogicalProject+"__user" || b.AppURN != b.LogicalProject+":_user" || seen[b.PhysicalProject] {
			return errors.New("invalid or repeated app-group custody")
		}
		seen[b.PhysicalProject] = true
		for _, d := range []string{b.SourceDigest, b.QualificationDigest, b.InstallationDigest, b.BindingDigest, b.RecipeDigest, b.EffectiveInputDigest} {
			if !groupSHA.MatchString(d) {
				return errors.New("app-group digest is unbound")
			}
		}
		if err := validateGroupGeneration(g, g.Original, false); err != nil {
			return err
		}
		if g.Original.Absent || g.Original.Partial || len(g.Original.Containers) == 0 {
			return errors.New("app-group requires original exact containers")
		}
		for _, c := range g.Original.Containers {
			if c.WasRunning != g.InitialRunning() {
				return errors.New("mixed app-group power remains unqualified")
			}
		}
		ids := map[string]bool{}
		previous := ""
		for i, e := range g.Effects {
			prefix := g
			prefix.Effects = g.Effects[:i]
			if !groupActionAllowed(prefix, e.Action) {
				return errors.New("app-group action order changed")
			}
			if !g.InitialRunning() {
				return errors.New("initially stopped group must not actuate")
			}
			if e.State != ControlPreparing && e.State != ControlPrepared && e.State != ControlDispatched && e.State != ControlAdopted && e.State != ControlObserved && e.State != ControlNegativeAdopted && e.State != ControlFailed {
				return errors.New("invalid app-group effect phase")
			}
			if e.State == ControlPreparing {
				if e.Handle != "" {
					return errors.New("preparing group has a handle")
				}
			} else if e.Handle == "" && !(e.Negative != nil && e.Negative.Kind == "undispatched") {
				return errors.New("app-group lost its exact handle")
			}
			settled := e.State == ControlAdopted || e.State == ControlObserved
			negative := e.State == ControlNegativeAdopted || e.State == ControlFailed
			if settled != (e.Receipt != nil) || negative != (e.Negative != nil) || (settled || negative) != (e.Generation != nil) {
				return errors.New("app-group receipt/generation differs from phase")
			}
			if i > 0 && !groupEffectSettled(g.Effects[i-1]) {
				return ErrControlUnresolved
			}
			if negative {
				n := e.Negative
				if !groupSHA.MatchString(n.WitnessDigest) || !groupSHA.MatchString(n.PreviousCustodyDigest) || n.Reason == "" {
					return errors.New("negative group proof incomplete")
				}
				if n.Kind == "undispatched" {
					proof := CloneAppGroups([]AppGroupControl{g})[0]
					proof.Effects = proof.Effects[:i+1]
					proof.Effects[i].State = n.DispatchPhase
					proof.Effects[i].Negative = nil
					proof.Effects[i].Generation = nil
					if (n.DispatchPhase != ControlPreparing && n.DispatchPhase != ControlPrepared) || n.WitnessDigest != AppGroupDigest(proof) {
						return errors.New("negative pre-dispatch witness differs from original signed intent")
					}

					if !n.NoNativeActuation || n.Phase != "pre-dispatch" || n.OperationID != "" || n.RequestDigest != "" || n.PredecessorOperationID != "" || n.ComposeExit != nil {
						return errors.New("invalid undispatched proof")
					}
				} else {
					if !groupID.MatchString(n.OperationID) || ids[n.OperationID] || n.PredecessorOperationID != previous || !groupSHA.MatchString(n.RequestDigest) {
						return errors.New("negative native lineage changed")
					}
					if n.Kind == "never-claimed" {
						if n.Phase != "admission" || n.Reason != "NEVER_CLAIMED" || !n.NoNativeActuation || n.ComposeExit != nil {
							return errors.New("invalid native non-claim proof")
						}
					} else if n.Kind == "command" {
						if n.Phase == "preparation" {
							if !n.NoNativeActuation || n.ComposeExit != nil {
								return errors.New("invalid native preparation proof")
							}
						} else if n.Phase == "child-closed" {
							if n.NoNativeActuation || n.ComposeExit == nil || *n.ComposeExit == 0 {
								return errors.New("invalid closed-child failure")
							}
						} else {
							return errors.New("unknown command failure phase")
						}
					} else {
						return errors.New("unknown negative witness kind")
					}
					ids[n.OperationID] = true
					previous = n.OperationID
				}
				if n.NoNativeActuation {
					if !reflect.DeepEqual(*e.Generation, prefix.Current()) {
						return errors.New("no-actuation proof changed preceding exact graph")
					}
				} else if !e.Generation.Absent && !e.Generation.Partial {
					return errors.New("closed failure needs explicit partial graph")
				}
				if err := validateGroupGeneration(g, *e.Generation, e.Generation.Absent); err != nil {
					return err
				}
			}
			if settled {
				if e.Generation.Partial {
					return errors.New("positive terminal actuation cannot adopt partial graph")
				}
				r := e.Receipt
				if !groupID.MatchString(r.OperationID) || r.PredecessorOperationID != previous || ids[r.OperationID] || !groupSHA.MatchString(r.RequestDigest) || !groupSHA.MatchString(r.WitnessDigest) || r.ComposeExit != 0 || !groupSHA.MatchString(r.PreviousCustodyDigest) {
					return errors.New("app-group native receipt lineage invalid")
				}
				ids[r.OperationID] = true
				previous = r.OperationID
				if err := validateGroupGeneration(g, *e.Generation, !controlStarts(e.Action)); err != nil {
					return err
				}
				if controlStarts(e.Action) {
					priorIDs := map[string]bool{}
					for _, c := range g.Original.Containers {
						priorIDs[c.ID] = true
					}
					for j := 0; j < i; j++ {
						if g.Effects[j].Generation != nil {
							for _, c := range g.Effects[j].Generation.Containers {
								priorIDs[c.ID] = true
							}
						}
					}
					for _, c := range e.Generation.Containers {
						if !c.WasRunning || priorIDs[c.ID] {
							return errors.New("START requires a running authorized successor generation")
						}
					}
				}
			}
		}
		if g.Closed && !AppGroupRestored(g) {
			return errors.New("app-group closed before power restoration")
		}
	}
	return nil
}
func validateGroupGeneration(g AppGroupControl, current AppGroupGeneration, absent bool) error {
	if current.Absent != absent || absent && (len(current.Containers) != 0 || current.Partial) || len(current.Volumes) == 0 {
		return errors.New("app-group presence or volume identity changed")
	}
	volumes := map[string]AppGroupVolume{}
	for _, v := range current.Volumes {
		if !groupVolumeName.MatchString(v.Name) || v.Mountpoint != "/var/lib/docker/volumes/"+v.Name+"/_data" || v.Driver != "local" || !groupSHA.MatchString(v.IdentityDigest) || volumes[v.Name].Name != "" {
			return errors.New("invalid exact external volume identity")
		}
		volumes[v.Name] = v
	}
	if len(g.Original.Volumes) > 0 && !reflect.DeepEqual(current.Volumes, g.Original.Volumes) {
		return errors.New("external volume identity changed across group generation")
	}
	if absent {
		if len(current.ConfigDigests) != 0 {
			return errors.New("absent generation retains runtime config")
		}
		return nil
	}
	anchor := map[string]SnapshotQuiescedContainer{}
	for _, c := range g.Original.Containers {
		anchor[c.ComponentRef] = c
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range current.Containers {
		if !groupSHA.MatchString(current.ConfigDigests[c.ComponentRef]) || current.ConfigDigests[c.ComponentRef] != g.Original.ConfigDigests[c.ComponentRef] {
			return errors.New("app-group config digest changed")
		}
		if !groupSHA.MatchString(c.ID) || c.ComponentRef == "" || seen[c.ComponentRef] || ids[c.ID] || c.ComposeProject != g.Binding.PhysicalProject || c.ComposeService != c.ComponentRef || c.WorkloadRef == "" || c.SiteRef != g.Binding.SiteRef || c.NodeRef != g.Binding.NodeRef || !groupPinnedImage.MatchString(c.Image) {
			return errors.New("app-group container graph is incomplete")
		}
		seen[c.ComponentRef] = true
		ids[c.ID] = true
		if old, ok := anchor[c.ComponentRef]; ok {
			a, b := old, c
			a.ID = ""
			b.ID = ""
			a.Name = ""
			b.Name = ""
			a.WasRunning = false
			b.WasRunning = false
			if !reflect.DeepEqual(a, b) {
				return errors.New("successor graph differs from original recipe or mounts")
			}
		}
		for _, m := range c.Mounts {
			if m.Type != "volume" || volumes[m.Name].Name == "" || m.Source != volumes[m.Name].Mountpoint || m.Destination == "" {
				return errors.New("app-group mount is outside exact external volumes")
			}
		}
	}
	if len(anchor) > 0 {
		for name := range seen {
			if _, ok := anchor[name]; !ok {
				return errors.New("successor has an unauthorized component")
			}
		}
	}
	if len(current.ConfigDigests) != len(seen) {
		return errors.New("app-group config coverage incomplete")
	}
	if len(anchor) > 0 && len(seen) != len(anchor) && !current.Partial {
		return errors.New("successor component set changed")
	}
	return nil
}
func AppGroupRestored(g AppGroupControl) bool {
	if !g.InitialRunning() {
		return len(g.Effects) == 0
	}
	if len(g.Effects) == 0 {
		return true
	}
	e := g.Effects[len(g.Effects)-1]
	if e.State == ControlFailed && e.Negative != nil && e.Negative.NoNativeActuation {
		g.Effects = g.Effects[:len(g.Effects)-1]
		return AppGroupRestored(g)
	}
	return controlStarts(e.Action) && e.State == ControlObserved
}
func AppGroupStopped(g AppGroupControl) bool {
	if !g.InitialRunning() {
		return len(g.Effects) == 0
	}
	if len(g.Effects) == 0 {
		return false
	}
	e := g.Effects[len(g.Effects)-1]
	if e.State == ControlFailed && e.Negative != nil && e.Negative.NoNativeActuation {
		g.Effects = g.Effects[:len(g.Effects)-1]
		return AppGroupStopped(g)
	}
	return !controlStarts(e.Action) && e.State == ControlObserved
}

// AdvanceAppGroup performs at most one dormant Prepare and one Dispatch. Every
// recovery first inspects original handles. No power/404/timeout is admission
// or completion proof. Adoption is signed atomically BEFORE the observed step.
func AdvanceAppGroup(ctx context.Context, g AppGroupControl, action string, persist func(AppGroupControl) error, d AppGroupControlDriver) (AppGroupControl, error) {
	if ctx == nil || persist == nil || d == nil || runtimeControlEffect(&RuntimeControl{}, action) == nil {
		return g, errors.New("app-group control bindings missing")
	}
	if err := ValidateAppGroups([]AppGroupControl{g}); err != nil {
		return g, err
	}
	if g.Closed {
		return g, errors.New("app-group hold already closed")
	}
	if !g.InitialRunning() {
		return g, errors.New("initially stopped group requires read-only stopped verification")
	}
	var err error
	g, err = SettleAppGroup(ctx, g, persist, d)
	if err != nil {
		return g, err
	}
	if len(g.Effects) > 0 {
		last := g.Effects[len(g.Effects)-1]
		if last.Action == action {
			if last.State == ControlObserved {
				return g, nil
			}
			if last.State == ControlFailed && action != "rollback-stop" {
				return g, ErrAppGroupFailed
			}
			if last.State != ControlFailed {
				return g, ErrControlUnresolved
			}
		}
	}
	if !groupActionAllowed(g, action) {
		return g, errors.New("app-group predecessor action changed")
	}
	g = CloneAppGroups([]AppGroupControl{g})[0]
	g.Effects = append(g.Effects, AppGroupEffect{Action: action, State: ControlPreparing})
	if err = persist(g); err != nil {
		return g, err
	}
	handle, err := d.Prepare(ctx, action)
	if err != nil || handle == "" {
		return g, errors.Join(ErrControlUnresolved, err)
	}
	i := len(g.Effects) - 1
	g.Effects[i].Handle = handle
	g.Effects[i].State = ControlPrepared
	if err = persist(g); err != nil {
		return g, err
	}
	g.Effects[i].State = ControlDispatched
	if err = persist(g); err != nil {
		return g, err
	}
	if err = d.Dispatch(ctx, handle, action); err != nil {
		return g, errors.Join(ErrControlUnresolved, err)
	}
	g, err = SettleAppGroup(ctx, g, persist, d)
	if err == nil && len(g.Effects) > 0 && g.Effects[len(g.Effects)-1].State == ControlFailed {
		err = ErrAppGroupFailed
	}
	return g, err
}
func SettleAppGroup(ctx context.Context, g AppGroupControl, persist func(AppGroupControl) error, d AppGroupControlDriver) (AppGroupControl, error) {
	if persist == nil || d == nil {
		return g, errors.New("app-group settlement bindings missing")
	}
	if err := ValidateAppGroups([]AppGroupControl{g}); err != nil {
		return g, err
	}
	g = CloneAppGroups([]AppGroupControl{g})[0]
	for i := range g.Effects {
		e := &g.Effects[i]
		if e.State == ControlPreparing || e.State == ControlPrepared || e.State == ControlDispatched {
			var negative *AppGroupNegative
			var generation AppGroupGeneration
			var receipt *AppGroupReceipt
			nd, canNegative := d.(AppGroupNegativeDriver)
			if e.State == ControlPreparing || e.State == ControlPrepared {
				if !canNegative {
					return g, ErrControlUnresolved
				}
				n, gen, err := nd.RecoverUndispatched(ctx, g, *e)
				if err != nil {
					return g, errors.Join(ErrControlUnresolved, err)
				}
				negative = &n
				generation = gen
			} else {
				ok, err := d.Complete(ctx, e.Handle, e.Action)
				if err == nil && ok {
					r, gen, err := d.Readback(ctx, g, e.Handle, e.Action)
					if err != nil {
						return g, err
					}
					receipt = &r
					generation = gen
				} else {
					if !canNegative {
						return g, errors.Join(ErrControlUnresolved, err)
					}
					n, gen, nerr := nd.ReadbackNegative(ctx, g, e.Handle, e.Action)
					if nerr != nil {
						return g, errors.Join(ErrControlUnresolved, err, nerr)
					}
					negative = &n
					generation = gen
				}
			}
			e.Receipt = receipt
			e.Negative = negative
			e.Generation = &generation
			e.State = ControlAdopted
			if negative != nil {
				e.State = ControlNegativeAdopted
			}
			if err := ValidateAppGroups([]AppGroupControl{g}); err != nil {
				return g, err
			}
			if err := persist(g); err != nil {
				return g, err
			}
		}
		if e.State == ControlNegativeAdopted {
			if err := d.Adopt(ctx, g); err != nil {
				return g, err
			}
			e.State = ControlFailed
			if err := persist(g); err != nil {
				return g, err
			}
		}

		if e.State == ControlAdopted {
			if err := d.Adopt(ctx, g); err != nil {
				return g, err
			}
			e.State = ControlObserved
			if err := persist(g); err != nil {
				return g, err
			}
		}
	}
	return g, nil
}

// ValidAppGroupTransition enforces append-only intent, immutable G0/binding,
// exact handle retention and one durable phase per owning-journal CAS.
func ValidAppGroupTransition(before, after []AppGroupControl) bool {
	if ValidateAppGroups(after) != nil || len(before) != len(after) {
		return false
	}
	rank := map[string]int{ControlPreparing: 1, ControlPrepared: 2, ControlDispatched: 3, ControlAdopted: 4, ControlObserved: 5, ControlNegativeAdopted: 4, ControlFailed: 5}
	changes := 0
	for i, a := range before {
		b := after[i]
		if a.Version != b.Version || !reflect.DeepEqual(a.Binding, b.Binding) || !reflect.DeepEqual(a.Original, b.Original) || a.Closed && !b.Closed {
			return false
		}
		if a.Closed != b.Closed {
			if !AppGroupRestored(b) || len(a.Effects) != len(b.Effects) {
				return false
			}
			changes++
		}
		if len(b.Effects) != len(a.Effects) && len(b.Effects) != len(a.Effects)+1 {
			return false
		}
		for j, x := range a.Effects {
			y := b.Effects[j]
			if reflect.DeepEqual(x, y) {
				continue
			}
			changes++
			if x.Action != y.Action || x.Handle != "" && x.Handle != y.Handle || !(rank[y.State] == rank[x.State]+1 || (x.State == ControlPreparing || x.State == ControlPrepared) && y.State == ControlNegativeAdopted) || x.State == ControlAdopted && y.State != ControlObserved || x.State == ControlNegativeAdopted && y.State != ControlFailed {
				return false
			}
			if y.State != ControlNegativeAdopted && x.State != ControlDispatched && (!reflect.DeepEqual(x.Receipt, y.Receipt) || !reflect.DeepEqual(x.Generation, y.Generation) || !reflect.DeepEqual(x.Negative, y.Negative)) {
				return false
			}
		}
		if len(b.Effects) > len(a.Effects) {
			e := b.Effects[len(b.Effects)-1]
			if e.State != ControlPreparing {
				return false
			}
			changes++
		}
	}
	return changes == 1
}
