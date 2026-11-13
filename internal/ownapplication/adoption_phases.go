package ownapplication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const (
	// adoptionRetention is the window in which --restore-original returns to
	// the original. The original is never deleted automatically.
	adoptionRetention = 7 * 24 * time.Hour
	// adoptionStabilityWindow is how long the rebuilt container must keep
	// running without a restart before the adoption counts as successful.
	adoptionStabilityWindow = 2 * time.Minute
	// adoptionCopyRate is the conservative copy rate the downtime estimate
	// assumes (bytes per second).
	adoptionCopyRate = 50 << 20
)

var operationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,100}$`)

// plan adds the node: the admitted document, its own-application review, the
// registry check, the data copy size and the expected downtime.
func plan(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest) (*adoptionPlan, error) {
	p, err := observe(ctx, docker, req)
	if err != nil {
		return nil, err
	}
	app := p.result.Application
	i := req.Installation
	app.Hostname = app.WorkloadRef() + "." + i.Domain
	if p.result.Data != nil {
		volume := localbackuppolicy.StandaloneComposeVolumeName(app.WorkloadRef(), i.NodeRef, "app", "data")
		p.result.Data.Volume = volume
		// The copy empties its destination first, so an existing volume is
		// only reused when it is the copy a rolled-back adoption of this same
		// container left. Retained data of any other application is refused.
		if _, err := docker.InspectVolume(ctx, volume); err == nil {
			prior, found, err := loadCheckpoint(workspace, app.Name)
			switch {
			case err == nil && found && prior.ContainerID == p.result.ContainerID && prior.DataVolume == volume && prior.Phase == "rolled-back":
				p.row("data_volume_replaced", "data-volume", RowChanged, volume, "replaced", "the copy a rolled-back adoption of this container left is replaced by a fresh copy", "volume", volume)
			default:
				p.refuse("refused_existing_application_volume", "data-volume", volume, "the application volume already exists and may hold retained data (for example of a removed or restored application); remove it yourself or choose another --name", "volume", volume)
			}
		}
	}
	admissible := !hasRefusal(p.result.Rows)
	if admissible {
		if err := app.Validate(); err != nil {
			p.refuse("refused_contract", "contract", "", err.Error())
			admissible = false
		}
	}
	if admissible {
		if err := nativehost.VerifyOwnApplicationImageDigest(ctx, app.Image); err != nil {
			p.refuse("refused_registry_image", "registry", app.Image, err.Error(), "image", app.Image)
			admissible = false
		}
	}
	if admissible {
		review := Request{Action: "create", Name: app.Name, Application: app, DryRun: true, SecretMaterial: secretMaterial(p.secrets)}
		if req.Offline {
			review = Request{Action: "render", Name: app.Name, Application: app, DryRun: true}
		}
		reviewed, err := Execute(ctx, workspace, i, review)
		switch {
		case err != nil && app.OutboundEnabled():
			p.refuse("refused_review_outbound", "review", "", err.Error()+"; to adopt without Internet access (published ports stay), plan again with --no-outbound")
			admissible = false
		case err != nil:
			p.refuse("refused_review", "review", "", err.Error())
			admissible = false
		default:
			p.result.ReviewDigest = reviewed.ReviewDigest
		}
	}
	var copySeconds int64
	if p.source != nil {
		switch {
		case req.Offline || req.HelperImage == "":
			p.volatile("data_size_not_measured", "data-size", RowKept, p.result.Data.Source, "", "not measured: an offline plan has no installed backup helper", "source", p.result.Data.Source)
		default:
			files, bytes, free, err := docker.Measure(ctx, req.HelperImage, "measure-"+app.Name, *p.source)
			switch {
			case err != nil:
				p.volatile("refused_data_unmeasurable", "data-size", RowRefused, p.result.Data.Source, "", "the data source could not be measured: "+err.Error(), "source", p.result.Data.Source)
				admissible = false
			case 2*bytes > free:
				p.volatile("refused_data_disk_space", "data-size", RowRefused, fmt.Sprintf("%d bytes", bytes), fmt.Sprintf("%d bytes free", free), "the copy needs at least twice its size free on the Docker volume filesystem", "bytes", strconv.FormatInt(bytes, 10), "freeBytes", strconv.FormatInt(free, 10))
				admissible = false
			default:
				p.result.Data.Measured, p.result.Data.Files, p.result.Data.Bytes, p.result.Data.FreeBytes = true, files, bytes, free
				copySeconds = 2 * bytes / adoptionCopyRate // copy plus checksum
				p.volatile("data_size_measured", "data-size", RowKept, fmt.Sprintf("%d bytes in %d files", bytes, files), fmt.Sprintf("%d bytes free (at least 2x needed)", free), "copied with the source mounted read-only and checked by checksum; the original volume is never changed or renamed", "bytes", strconv.FormatInt(bytes, 10), "files", strconv.FormatInt(files, 10), "freeBytes", strconv.FormatInt(free, 10))
			}
		}
	}
	downtime := 30 + copySeconds + 60
	p.result.DowntimeSeconds = downtime
	p.volatile("downtime_estimated", "downtime", RowChanged, "running", fmt.Sprintf("about %d s, at most %d s before rollback", downtime, downtime+int64(adoptionStabilityWindow.Seconds())), "stop (up to 30 s), data copy and checksum, start and HTTP readiness (up to 60 s); the new container then has to stay up for a 2-minute stability window", "seconds", strconv.FormatInt(downtime, 10), "maxSeconds", strconv.FormatInt(downtime+int64(adoptionStabilityWindow.Seconds()), 10))
	p.result.Rows = append(p.result.Rows, readinessRow(ctx, p, app.HTTPPort))
	p.result.Admissible = admissible
	p.result.PlanDigest = planDigest(p, req)
	p.result.Outcome = "planned"
	if admissible {
		p.result.Notice = "Nothing has changed yet. Adopting stops the original and recreates it as a new container (new container ID) with about " + strconv.FormatInt(downtime, 10) + " s of downtime. Run --phase adopt --plan-digest " + p.result.PlanDigest + " --operation-id <id> --owner-approve; on any failure the original starts again."
	} else {
		p.result.Outcome = "refused"
		p.result.Notice = "Not admissible: see the refused rows. Nothing has changed."
	}
	if req.Offline {
		p.result.Notice += " Offline plan: adoption needs the installed node."
	}
	return p, nil
}

func (p *adoptionPlan) volatile(code, field, status, observed, adopted, reason string, values ...string) {
	p.result.Rows = append(p.result.Rows, AdoptionRow{Code: code, Values: rowValues(values), Field: field, Status: status, Observed: observed, Adopted: adopted, Reason: reason, volatile: true})
}

// readinessRow probes the original as the rebuilt application will be
// probed: HTTP 2xx/3xx on / at the routed port, no redirects followed.
func readinessRow(ctx context.Context, p *adoptionPlan, port int) AdoptionRow {
	row := AdoptionRow{Code: "readiness_not_probed", Values: map[string]string{"port": strconv.Itoa(port)}, Field: "readiness", Status: RowKept, Reason: "the application must answer HTTP 2xx/3xx on / at its web port before the route opens", volatile: true}
	address, err := netip.ParseAddr(p.address)
	if !p.running || port == 0 || err != nil || !address.IsPrivate() {
		row.Observed = "not probed (original not running or has no private bridge address)"
		return row
	}
	status, err := probeHTTP(ctx, net.JoinHostPort(address.String(), strconv.Itoa(port)))
	if err != nil {
		row.Code, row.Status, row.Observed = "readiness_no_answer", RowChanged, "no answer from the original: "+err.Error()
		row.Reason += "; adopt fails safely and restarts the original if the rebuilt one does not answer"
		return row
	}
	row.Code, row.Observed = "readiness_answered", "original answered HTTP "+strconv.Itoa(status)
	row.Values["httpStatus"] = strconv.Itoa(status)
	if status < 200 || status >= 400 {
		row.Code, row.Status = "readiness_wrong_status", RowChanged
		row.Reason += "; this original does not, so adopt would fail safely and restart it — choose another --http-port"
	}
	return row
}

func probeHTTP(ctx context.Context, hostPort string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostPort+"/", nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	response.Body.Close()
	return response.StatusCode, nil
}

func planDigest(p *adoptionPlan, req AdoptionRequest) string {
	rows := []AdoptionRow{}
	for _, row := range p.result.Rows {
		if !row.volatile {
			rows = append(rows, row)
		}
	}
	return hash(struct {
		ContainerID  string
		Application  *contract.Application
		HiddenKeys   []string
		SecretKeys   []string
		Rows         []AdoptionRow
		Source       *nativehost.OwnAdoptionMount
		Installation contract.Installation
		HelperImage  string
		Offline      bool
	}{p.result.ContainerID, p.result.Application, p.result.HiddenKeys, p.result.SecretKeys, rows, p.source, req.Installation, req.HelperImage, req.Offline})
}

func adopt(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest, hooks AdoptionHooks) (AdoptionResult, error) {
	if !req.OwnerApproved || !operationID.MatchString(req.OperationID) {
		return AdoptionResult{}, errors.New("adopt requires --owner-approve and a stable --operation-id")
	}
	if req.Offline || req.HelperImage == "" {
		return AdoptionResult{}, errors.New("adopt runs only on the installed node with its backup helper")
	}
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(req.PlanDigest) {
		return AdoptionResult{}, errors.New("adopt requires the --plan-digest of the reviewed plan; nothing is stopped without it")
	}
	// A retried, completed adoption answers from its checkpoint: after it
	// the original may be released and the application exists, so a new plan
	// would no longer match.
	if req.Name != "" {
		if prior, found, err := loadCheckpoint(workspace, req.Name); err == nil && found && prior.OperationID == req.OperationID && prior.PlanDigest == req.PlanDigest && (prior.Phase == "adopted" || prior.Phase == "released") {
			result := emptyAdoptionResult("adopt")
			result.Checkpoint, result.Name, result.ContainerID, result.Outcome, result.Admissible = &prior, prior.Name, prior.ContainerID, "adopted", true
			result.PlanDigest = prior.PlanDigest
			if !prior.RetainUntil.IsZero() {
				result.RetainUntil = &prior.RetainUntil
			}
			result.Notice = "Already adopted by this operation."
			return result, nil
		}
	}
	planned := req
	planned.Phase = "plan"
	p, err := plan(ctx, workspace, docker, planned)
	if err != nil {
		return AdoptionResult{}, err
	}
	result := p.result
	result.Phase = "adopt"
	app := *result.Application
	if prior, found, err := loadCheckpoint(workspace, app.Name); err != nil {
		return result, err
	} else if found && prior.Phase != "rolled-back" && prior.Phase != "released" && prior.Phase != "restored" {
		if prior.Phase == "adopted" {
			return result, fmt.Errorf("%s is already adopted from container %s; restore or release that adoption first", app.Name, prior.ContainerID[:12])
		}
		return result, fmt.Errorf("adoption %s of %s stopped at %s; recover the original with --restore-original before a new adoption", prior.OperationID, app.Name, prior.Phase)
	}
	if result.PlanDigest != req.PlanDigest {
		result.Outcome = "refused"
		return result, errors.New("the plan is stale: the container, node or options changed since review; plan again (nothing was stopped)")
	}
	if !result.Admissible {
		result.Outcome = "refused"
		return result, errors.New("the plan is not admissible; see the refused rows (nothing was stopped)")
	}
	checkpoint := AdoptionCheckpoint{APIVersion: adoptionCheckpointVersion, Name: app.Name, ContainerID: result.ContainerID, PlanDigest: req.PlanDigest, OperationID: req.OperationID, OriginalRunning: p.running, OriginalRestartPolicy: p.restart, Phase: "stopping-original", At: time.Now().UTC()}
	if result.Data != nil {
		checkpoint.DataSource, checkpoint.DataVolume = result.Data.Source, result.Data.Volume
	}
	if hooks.BeforeCreate != nil {
		if err := hooks.BeforeCreate(app); err != nil {
			result.Outcome = "refused"
			return result, err
		}
	}
	if err := saveCheckpoint(workspace, checkpoint); err != nil {
		return result, err
	}
	result.Checkpoint = &checkpoint
	created := false
	fail := func(cause error) (AdoptionResult, error) {
		recovery := rollbackAdoption(ctx, workspace, docker, req, app, checkpoint, created, hooks)
		checkpoint.Phase, checkpoint.Error, checkpoint.At = "rolled-back", cause.Error(), time.Now().UTC()
		result.Outcome = "rolled_back"
		if recovery != nil {
			checkpoint.Phase, checkpoint.Error = "rollback-failed", cause.Error()+"; rollback: "+recovery.Error()
			result.Outcome = "rollback_failed"
		}
		_ = saveCheckpoint(workspace, checkpoint)
		result.Checkpoint = &checkpoint
		if recovery != nil {
			return result, fmt.Errorf("rollback_failed: adoption failed (%w) and the rollback is incomplete: %v; the original is retained; retry the recovery with --restore-original", cause, recovery)
		}
		return result, fmt.Errorf("rolled_back: adoption failed and was rolled back; the original runs as before: %w", cause)
	}

	// Stop the original and keep it from returning after a reboot. It is
	// never edited otherwise, and its volumes are never changed or renamed.
	if err := docker.SetRestartPolicy(ctx, result.ContainerID, "no"); err != nil {
		return fail(fmt.Errorf("disable the original's restart policy: %w", err))
	}
	if err := docker.Stop(ctx, result.ContainerID); err != nil {
		return fail(fmt.Errorf("stop the original: %w", err))
	}
	if p.source != nil {
		checkpoint.Phase = "copying-data"
		if err := saveCheckpoint(workspace, checkpoint); err != nil {
			return fail(err)
		}
		project := localbackuppolicy.StandaloneComposeProjectName(app.WorkloadRef(), req.Installation.NodeRef)
		if _, err := docker.InspectVolume(ctx, result.Data.Volume); err != nil {
			if err = docker.CreateComposeVolume(ctx, result.Data.Volume, project, "app-data"); err != nil {
				return fail(fmt.Errorf("create the application volume: %w", err))
			}
		}
		label := "copy-" + app.Name
		if err := docker.Copy(ctx, req.HelperImage, label, *p.source, result.Data.Volume); err != nil {
			return fail(fmt.Errorf("copy data: %w", err))
		}
		before, err := docker.Digest(ctx, req.HelperImage, label, *p.source)
		if err != nil {
			return fail(fmt.Errorf("checksum the source: %w", err))
		}
		after, err := docker.Digest(ctx, req.HelperImage, label, nativehost.OwnAdoptionMount{Type: "volume", Source: result.Data.Volume})
		if err != nil {
			return fail(fmt.Errorf("checksum the copy: %w", err))
		}
		if before != after {
			return fail(fmt.Errorf("the copy differs from its source (%s != %s)", after, before))
		}
		checkpoint.DataDigest = after
		result.Data.Digest = after
	}

	checkpoint.Phase = "creating-application"
	if err := saveCheckpoint(workspace, checkpoint); err != nil {
		return fail(err)
	}
	created = true
	review, err := Execute(ctx, workspace, req.Installation, Request{Action: "create", Name: app.Name, Application: &app, DryRun: true, SecretMaterial: secretMaterial(p.secrets)})
	if err != nil {
		return fail(fmt.Errorf("review the application: %w", err))
	}
	own, err := Execute(ctx, workspace, req.Installation, Request{Action: "create", Name: app.Name, Application: &app, OperationID: req.OperationID, ReviewDigest: review.ReviewDigest, OwnerApproved: true, SecretMaterial: secretMaterial(p.secrets)})
	if hooks.AfterMutation != nil {
		hooks.AfterMutation()
	}
	if err != nil {
		return fail(fmt.Errorf("create the application: %w", err))
	}
	result.ReviewDigest = review.ReviewDigest

	checkpoint.Phase = "verifying"
	if err := saveCheckpoint(workspace, checkpoint); err != nil {
		return fail(err)
	}
	if err := verifyRebuilt(ctx, workspace, docker, req.Installation, app, adoptionStabilityWindow, &result); err != nil {
		return fail(fmt.Errorf("verify the application: %w", err))
	}
	now := time.Now().UTC()
	checkpoint.Phase, checkpoint.At, checkpoint.RetainUntil = "adopted", now, now.Add(adoptionRetention)
	if err := saveCheckpoint(workspace, checkpoint); err != nil {
		return result, err
	}
	result.Checkpoint, result.OwnApplication, result.Outcome, result.RetainUntil = &checkpoint, &own, "adopted", &checkpoint.RetainUntil
	result.OriginalState = "stopped and retained (restart policy no)"
	result.Notice = "Adopted: the application runs as own-" + app.Name + " with the own-application lifecycle (app own --action update|rollback|stop|remove|status|logs, backup). The original is stopped and retained until " + checkpoint.RetainUntil.Format(time.RFC3339) + " for --restore-original; --release-original removes it after your confirmation. Nothing is deleted automatically."
	return result, nil
}

// verifyRebuilt requires the signed own-application status, an answer on
// every published TCP port, and a rebuilt container that keeps running
// without a restart for the stability window. "Running" alone is not success.
func verifyRebuilt(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, installation contract.Installation, app contract.Application, window time.Duration, result *AdoptionResult) error {
	status, err := Execute(ctx, workspace, installation, Request{Action: "status", Name: app.Name})
	if err != nil {
		return err
	}
	if status.ObservationError != "" || len(status.Observation) == 0 {
		return errors.New("no runtime observation: " + status.ObservationError)
	}
	project := localbackuppolicy.StandaloneComposeProjectName(app.WorkloadRef(), installation.NodeRef)
	ids, err := docker.ComposePeers(ctx, project)
	if err != nil || len(ids) != 1 {
		return fmt.Errorf("the rebuilt container of %s is not observable", project)
	}
	first, err := containerHealth(ctx, docker, ids[0])
	if err != nil {
		return err
	}
	deadline := time.Now().Add(window)
	for {
		current, err := containerHealth(ctx, docker, ids[0])
		if err != nil {
			return err
		}
		if !current.running || current.restarts != first.restarts {
			return fmt.Errorf("the rebuilt container did not stay up (state %s, restarts %d -> %d)", current.status, first.restarts, current.restarts)
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(10*time.Second, time.Until(deadline)+time.Millisecond)):
		}
	}
	result.PortProbes = probePublishedPorts(ctx, app)
	for address, outcome := range result.PortProbes {
		if !strings.HasPrefix(outcome, "answers") {
			return fmt.Errorf("published port %s does not answer: %s", address, outcome)
		}
	}
	return nil
}

type containerHealthObservation struct {
	running  bool
	status   string
	restarts int
}

func containerHealth(ctx context.Context, docker nativehost.OwnAdoptionDocker, id string) (containerHealthObservation, error) {
	raw, err := docker.InspectContainer(ctx, id)
	if err != nil {
		return containerHealthObservation{}, err
	}
	var containers []struct {
		ID           string `json:"Id"`
		RestartCount int    `json:"RestartCount"`
		State        struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
		} `json:"State"`
	}
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 || containers[0].ID != id {
		return containerHealthObservation{}, errors.New("rebuilt container inspection is malformed")
	}
	c := containers[0]
	return containerHealthObservation{running: c.State.Running, status: c.State.Status, restarts: c.RestartCount}, nil
}

// rollbackAdoption removes the new workload when one was started and starts
// the original again with its original restart policy.
func rollbackAdoption(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest, app contract.Application, checkpoint AdoptionCheckpoint, created bool, hooks AdoptionHooks) error {
	ctx = context.WithoutCancel(ctx)
	var problems []string
	if created {
		if err := removeAdopted(ctx, workspace, req, app.Name, checkpoint.ContainerID, req.OperationID+".rollback", hooks); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if err := docker.SetRestartPolicy(ctx, checkpoint.ContainerID, checkpoint.OriginalRestartPolicy); err != nil {
		problems = append(problems, "restore the original's restart policy: "+err.Error())
	}
	if checkpoint.OriginalRunning {
		if err := docker.Start(ctx, checkpoint.ContainerID); err != nil {
			problems = append(problems, "start the original: "+err.Error())
		}
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// removeAdopted removes the rebuilt application through the reviewed
// own-application remove. Its named volume is retained (backup keeps it).
// It removes only an application adopted from exactly this container: an
// unrelated application of the same name is never touched.
func removeAdopted(ctx context.Context, workspace string, req AdoptionRequest, name, containerID, operation string, hooks AdoptionHooks) error {
	state, err := Load(workspace)
	if err != nil {
		return errors.New("read own-application custody: " + err.Error())
	}
	record, exists := state.Applications[name]
	if !exists || record.State == "removed" {
		return nil
	}
	adopted := record.Bundle.Application
	if !adopted.IsAdopted() || adopted.AdoptedFrom == nil || adopted.AdoptedFrom.ContainerID != containerID {
		return nil
	}
	review, err := Execute(ctx, workspace, req.Installation, Request{Action: "remove", Name: name, DryRun: true})
	if err == nil {
		_, err = Execute(ctx, workspace, req.Installation, Request{Action: "remove", Name: name, OperationID: operation, ReviewDigest: review.ReviewDigest, OwnerApproved: true})
	}
	if hooks.AfterMutation != nil {
		hooks.AfterMutation()
	}
	if err != nil {
		return errors.New("remove the rebuilt application: " + err.Error())
	}
	return nil
}

func verifyAdoption(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest) (AdoptionResult, error) {
	result := emptyAdoptionResult("verify")
	checkpoint, err := checkpointForContainer(ctx, workspace, docker, req)
	if err != nil {
		return result, err
	}
	result.Checkpoint, result.Name, result.ContainerID = &checkpoint, checkpoint.Name, checkpoint.ContainerID
	if !checkpoint.RetainUntil.IsZero() {
		result.RetainUntil = &checkpoint.RetainUntil
	}
	result.OriginalState = originalState(ctx, docker, checkpoint.ContainerID)
	state, err := Load(workspace)
	if err != nil {
		return result, err
	}
	record, exists := state.Applications[checkpoint.Name]
	if !exists || !record.Bundle.Application.IsAdopted() || record.Bundle.Application.AdoptedFrom.ContainerID != checkpoint.ContainerID {
		result.Outcome = outcomeForPhase(checkpoint.Phase)
		result.Notice = "No adopted application is recorded for this container (adoption " + checkpoint.Phase + ")."
		return result, nil
	}
	app := record.Bundle.Application
	result.Application = &app
	result.OutboundInternet = app.OutboundEnabled()
	if app.PublishedPorts != nil {
		result.PublishedPorts = app.PublishedPorts
	}
	result.Outcome = outcomeForPhase(checkpoint.Phase)
	if checkpoint.Phase == "adopted" && record.State == "running" {
		// A short window: verify is a read; adopt already held the full one.
		if err := verifyRebuilt(ctx, workspace, docker, req.Installation, app, 10*time.Second, &result); err != nil {
			result.Outcome = "unhealthy"
			result.Notice = "Adopted, but the application is not healthy: " + err.Error()
			return result, nil
		}
		result.Admissible, result.Outcome = true, "verified"
	}
	result.Notice = "Adoption " + checkpoint.Phase + "; application " + record.State + "; original " + result.OriginalState + "."
	return result, nil
}

func outcomeForPhase(phase string) string {
	switch phase {
	case "rolled-back":
		return "rolled_back"
	case "rollback-failed":
		return "rollback_failed"
	case "adopted", "released", "restored":
		return phase
	case "":
		return "unknown"
	}
	return "in_progress"
}

// restoreOriginal returns to the original within the retention window: the
// rebuilt application is removed (its volume retained) and the original gets
// its restart policy back and starts when it ran before. Data written since
// the adoption stays only in the retained volume of the rebuilt application.
// It also recovers an interrupted adoption or a failed rollback.
func restoreOriginal(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest, hooks AdoptionHooks) (AdoptionResult, error) {
	result := emptyAdoptionResult("restore-original")
	if !req.OwnerApproved || !operationID.MatchString(req.OperationID) {
		return result, errors.New("--restore-original requires --owner-approve and a stable --operation-id")
	}
	checkpoint, err := checkpointForContainer(ctx, workspace, docker, req)
	if err != nil {
		return result, err
	}
	result.Checkpoint, result.Name, result.ContainerID = &checkpoint, checkpoint.Name, checkpoint.ContainerID
	switch checkpoint.Phase {
	case "restored", "rolled-back":
		result.Outcome, result.OriginalState = outcomeForPhase(checkpoint.Phase), originalState(ctx, docker, checkpoint.ContainerID)
		result.Notice = "The original is already back (" + checkpoint.Phase + ")."
		return result, nil
	case "released":
		return result, errors.New("the original was released (removed); there is nothing to restore")
	case "adopted":
		if time.Now().After(checkpoint.RetainUntil) {
			return result, fmt.Errorf("the retention window ended at %s; the original is still kept, but restoring it now would discard more than a week of the application's changes; adopt again or release it", checkpoint.RetainUntil.Format(time.RFC3339))
		}
		if state := originalState(ctx, docker, checkpoint.ContainerID); state != "exited" && state != "created" {
			return result, fmt.Errorf("the original is %s; only a stopped original is restored", state)
		}
	default:
		// An interrupted adoption (process killed, host rebooted) or a failed
		// rollback: recover exactly as the in-process rollback would have.
	}
	operation := req.OperationID + ".restore"
	if err := removeAdopted(ctx, workspace, req, checkpoint.Name, checkpoint.ContainerID, operation, hooks); err != nil {
		return result, err
	}
	if err := docker.SetRestartPolicy(ctx, checkpoint.ContainerID, checkpoint.OriginalRestartPolicy); err != nil {
		return result, fmt.Errorf("restore the original's restart policy: %w", err)
	}
	if checkpoint.OriginalRunning {
		if err := docker.Start(ctx, checkpoint.ContainerID); err != nil {
			return result, fmt.Errorf("start the original: %w", err)
		}
	}
	recovered := checkpoint.Phase != "adopted"
	checkpoint.Phase, checkpoint.At = "restored", time.Now().UTC()
	if recovered {
		checkpoint.Phase = "rolled-back"
	}
	if err := saveCheckpoint(workspace, checkpoint); err != nil {
		return result, err
	}
	result.Outcome, result.OriginalState = outcomeForPhase(checkpoint.Phase), originalState(ctx, docker, checkpoint.ContainerID)
	result.Notice = "The original is back with its own restart policy. The rebuilt application is removed; its named volume " + checkpoint.DataVolume + " is retained"
	if !recovered {
		result.Notice += " with any data written since the adoption"
	}
	result.Notice += "."
	return result, nil
}

// releaseOriginal removes the retained original once the adopted application
// runs. Its volumes and bind sources stay; nothing else is deleted.
func releaseOriginal(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest) (AdoptionResult, error) {
	result := emptyAdoptionResult("release-original")
	if !req.OwnerApproved || !operationID.MatchString(req.OperationID) {
		return result, errors.New("--release-original requires --owner-approve and a stable --operation-id")
	}
	checkpoint, err := checkpointForContainer(ctx, workspace, docker, req)
	if err != nil {
		return result, err
	}
	result.Checkpoint, result.Name, result.ContainerID = &checkpoint, checkpoint.Name, checkpoint.ContainerID
	if checkpoint.Phase == "released" {
		result.Outcome, result.OriginalState, result.Notice = "released", "removed", "The original was already released."
		return result, nil
	}
	if checkpoint.Phase != "adopted" {
		return result, fmt.Errorf("adoption is %s; only an adopted application releases its original", checkpoint.Phase)
	}
	// The signed custody, not the checkpoint, proves the adoption.
	state, err := Load(workspace)
	if err != nil {
		return result, err
	}
	record, exists := state.Applications[checkpoint.Name]
	if !exists || record.State != "running" || record.Operation.Status != "succeeded" || !record.Bundle.Application.IsAdopted() || record.Bundle.Application.AdoptedFrom.ContainerID != checkpoint.ContainerID {
		return result, errors.New("the adopted application is not running from this container in signed custody; the original stays")
	}
	if state := originalState(ctx, docker, checkpoint.ContainerID); state != "exited" && state != "created" {
		return result, fmt.Errorf("the original is %s; only a stopped original is released", state)
	}
	if err := docker.Remove(ctx, checkpoint.ContainerID); err != nil {
		return result, fmt.Errorf("remove the original: %w", err)
	}
	checkpoint.Phase, checkpoint.At = "released", time.Now().UTC()
	if err := saveCheckpoint(workspace, checkpoint); err != nil {
		return result, err
	}
	result.Outcome, result.OriginalState = "released", "removed"
	result.Notice = "The original container is removed. Its data source (" + checkpoint.DataSource + ") is kept; delete it yourself when you no longer need it."
	return result, nil
}

func emptyAdoptionResult(phase string) AdoptionResult {
	return AdoptionResult{APIVersion: AdoptionResultVersion, Phase: phase, Rows: []AdoptionRow{}, HiddenKeys: []string{}, SecretKeys: []string{}, PublishedPorts: []contract.PublishedPort{}}
}

func checkpointForContainer(ctx context.Context, workspace string, docker nativehost.OwnAdoptionDocker, req AdoptionRequest) (AdoptionCheckpoint, error) {
	if req.Name != "" {
		checkpoint, found, err := loadCheckpoint(workspace, req.Name)
		if err != nil {
			return checkpoint, err
		}
		if !found {
			return checkpoint, errors.New("no adoption is recorded for this name")
		}
		if req.Container != "" && !matchesContainer(ctx, docker, req.Container, checkpoint.ContainerID) {
			return checkpoint, errors.New("the container differs from the recorded adoption")
		}
		return checkpoint, nil
	}
	checkpoints, err := listCheckpoints(workspace)
	if err != nil {
		return AdoptionCheckpoint{}, err
	}
	for _, checkpoint := range checkpoints {
		if matchesContainer(ctx, docker, req.Container, checkpoint.ContainerID) {
			return checkpoint, nil
		}
	}
	return AdoptionCheckpoint{}, errors.New("no adoption is recorded for this container; pass --name")
}

// matchesContainer reports whether ref (ID, unique ID prefix or name) selects
// the recorded container. A released original no longer exists, so its full
// or prefix ID still matches without Docker.
func matchesContainer(ctx context.Context, docker nativehost.OwnAdoptionDocker, ref, id string) bool {
	if ref == id || (len(ref) >= 12 && strings.HasPrefix(id, ref)) {
		return true
	}
	raw, err := docker.InspectContainer(ctx, ref)
	if err != nil {
		return false
	}
	var containers []observedContainer
	return json.Unmarshal(raw, &containers) == nil && len(containers) == 1 && containers[0].ID == id
}

func originalState(ctx context.Context, docker nativehost.OwnAdoptionDocker, id string) string {
	raw, err := docker.InspectContainer(ctx, id)
	if err != nil {
		return "absent"
	}
	var containers []observedContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 || containers[0].ID != id {
		return "unknown"
	}
	return containers[0].State.Status
}

// probePublishedPorts reports whether each published TCP host port answers
// on this host: HTTP for the web port, a TCP connection for the others.
func probePublishedPorts(ctx context.Context, app contract.Application) map[string]string {
	probes := map[string]string{}
	for _, port := range app.PublishedPorts {
		if port.Protocol != "tcp" {
			continue
		}
		host := port.HostIP
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		address := net.JoinHostPort(host, strconv.Itoa(port.HostPort))
		if port.ContainerPort == app.HTTPPort {
			status, err := probeHTTP(ctx, address)
			switch {
			case err != nil:
				probes[address] = "no answer: " + err.Error()
			case status >= 200 && status < 400:
				probes[address] = "answers HTTP " + strconv.Itoa(status)
			default:
				probes[address] = "unexpected HTTP " + strconv.Itoa(status)
			}
			continue
		}
		dialer := net.Dialer{Timeout: 3 * time.Second}
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			probes[address] = "no connection: " + err.Error()
			continue
		}
		connection.Close()
		probes[address] = "answers TCP"
	}
	return probes
}
