package engine

// Drain and undrain (kombify fork addition, see KOMBIFY-FORK.md).
//
// Draining stops the writers of a planned move on the source: its
// applications and services, through Coolify's own stop actions, so Coolify
// shows them stopped and does not restart them. Databases keep running, so
// the final backup dumps them consistently with no writer left. The drain
// record keeps what ran before, so the backup still restores every drained
// resource as running on the destination and an undrain restarts exactly
// those on the source (the rollback). Nothing is deleted.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
)

// DrainFormat names the drain record document.
const DrainFormat = "kombify.io/coolify-relocation-drain/v1"

// DrainedResource is one resource a drain stopped or found stopped.
type DrainedResource struct {
	UUID  string `json:"uuid"`
	Table string `json:"table"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	// WasRunning: it ran before the drain; undrain restarts only these.
	WasRunning bool `json:"was_running"`
	// Services are the compose services that were up before the drain.
	Services []string `json:"services"`
	Stopped  bool     `json:"stopped"`
	Error    string   `json:"error,omitempty"`
}

// DrainRecord is the source side's state between drain and cutover.
type DrainRecord struct {
	Format    string            `json:"format"`
	Tool      ToolInfo          `json:"tool"`
	DrainedAt time.Time         `json:"drained_at"`
	Server    string            `json:"server"`
	Resources []DrainedResource `json:"resources"`
}

// Complete reports whether every resource that ran was stopped.
func (r *DrainRecord) Complete() bool {
	for _, d := range r.Resources {
		if d.WasRunning && !d.Stopped {
			return false
		}
	}
	return true
}

// Running returns the compose services per drained resource that ran before
// the drain, for a backup taken while they are stopped.
func (r *DrainRecord) Running() map[string][]string {
	out := map[string][]string{}
	for _, d := range r.Resources {
		if d.WasRunning && len(d.Services) > 0 {
			out[d.UUID] = d.Services
		}
	}
	return out
}

// Drain stops the applications and services of plan p on this server.
func Drain(ctx context.Context, in *coolify.Instance, p *RelocationPlan) (*DrainRecord, error) {
	if p == nil || p.Format != PlanFormat {
		return nil, errors.New("not a coolify relocation plan (format " + PlanFormat + ")")
	}
	if p.Manifest.Source.Hostname != "" && in.Hostname != "" && p.Manifest.Source.Hostname != in.Hostname {
		return nil, fmt.Errorf("the plan was made on %s, not on this server (%s): drain runs on the source", p.Manifest.Source.Hostname, in.Hostname)
	}
	here, err := in.ListResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list the resources of this Coolify: %w", err)
	}
	byUUID := map[string]coolify.Resource{}
	for _, r := range here {
		byUUID[r.UUID] = r
	}
	rec := &DrainRecord{Format: DrainFormat, Tool: Tool(), DrainedAt: time.Now().UTC(), Server: in.Hostname, Resources: []DrainedResource{}}
	var stop []map[string]string
	for _, planned := range p.Manifest.Resources {
		if !planned.Local() || planned.IsDatabase() {
			continue
		}
		r, ok := byUUID[planned.UUID]
		if !ok {
			return nil, fmt.Errorf("%s %q of the plan is not on this server any more", planned.Label(), planned.Name)
		}
		up, running, err := upServices(ctx, r.UUID)
		if err != nil {
			return nil, err
		}
		d := DrainedResource{UUID: r.UUID, Table: r.Table, Kind: r.Label(), Name: r.Name, WasRunning: r.Running() || running, Services: up}
		if !d.WasRunning {
			d.Stopped = true
		} else {
			stop = append(stop, map[string]string{"uuid": r.UUID, "table": r.Table})
		}
		rec.Resources = append(rec.Resources, d)
	}
	if len(stop) == 0 {
		return rec, nil
	}
	var res struct {
		Items []struct {
			UUID  string `json:"uuid"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"items"`
	}
	if err := in.PHP(ctx, "stop", stop, &res); err != nil {
		return nil, fmt.Errorf("Coolify could not stop the resources: %w", err)
	}
	failed := map[string]string{}
	for _, it := range res.Items {
		if !it.OK {
			failed[it.UUID] = it.Error
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	for i := range rec.Resources {
		d := &rec.Resources[i]
		if d.Stopped {
			continue
		}
		if msg, bad := failed[d.UUID]; bad {
			d.Error = msg
			continue
		}
		for {
			_, running, err := upServices(ctx, d.UUID)
			if err == nil && !running {
				d.Stopped = true
				break
			}
			if time.Now().After(deadline) {
				d.Error = "containers still running after the stop request"
				break
			}
			select {
			case <-ctx.Done():
				return rec, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	return rec, nil
}

// Undrain restarts on this server the resources rec stopped, through Coolify,
// and waits until they run again.
func Undrain(ctx context.Context, in *coolify.Instance, rec *DrainRecord, pr *Progress) ([]StartResult, error) {
	if rec == nil || rec.Format != DrainFormat {
		return nil, errors.New("not a coolify relocation drain record (format " + DrainFormat + ")")
	}
	if rec.Server != "" && in.Hostname != "" && rec.Server != in.Hostname {
		return nil, fmt.Errorf("the drain record is from %s, not from this server (%s)", rec.Server, in.Hostname)
	}
	here, err := in.ListResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list the resources of this Coolify: %w", err)
	}
	byUUID := map[string]coolify.Resource{}
	for _, r := range here {
		byUUID[r.UUID] = r
	}
	var todo []dbx.PlannedResource
	for _, d := range rec.Resources {
		if !d.WasRunning {
			continue
		}
		r, ok := byUUID[d.UUID]
		if !ok {
			return nil, fmt.Errorf("%s %q is not on this server any more", d.Kind, d.Name)
		}
		pl := dbx.PlannedResource{Resource: r, SourceUUID: r.UUID, WasRunning: true, Expect: d.Services}
		if r.Table == "applications" {
			// Its images are tagged with the commit of its last successful
			// deployment, so it starts again without a build.
			pl.Commit, _ = in.Scalar(ctx, "SELECT q.commit FROM application_deployment_queues q JOIN applications a ON a.id::text = q.application_id"+
				" WHERE a.uuid = "+coolify.SQLString(r.UUID)+" AND q.status = 'finished' AND q.pull_request_id = 0 ORDER BY q.id DESC LIMIT 1")
		}
		todo = append(todo, pl)
	}
	if len(todo) == 0 {
		return nil, nil
	}
	return StartResources(ctx, in, todo, pr)
}

// upServices lists the compose services of a resource as a backup records
// them: up, starting or a one-shot job that finished. running reports
// whether any container still runs.
func upServices(ctx context.Context, uuid string) (up []string, running bool, err error) {
	ds, err := resourceContainers(ctx, uuid)
	if err != nil {
		return nil, false, err
	}
	for _, d := range ds {
		svc := d.Config.Labels["com.docker.compose.service"]
		if svc == "" {
			svc = strings.TrimPrefix(d.Name, "/")
		}
		switch containerState(d) {
		case stateUp, stateStarting:
			up, running = append(up, svc), true
		case stateDone:
			up = append(up, svc)
		}
	}
	sort.Strings(up)
	return uniq(up), running, nil
}
