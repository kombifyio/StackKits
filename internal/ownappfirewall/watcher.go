package ownappfirewall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const watchRoot = "/run/stackkit-own-egress"
const localDocker = "unix:///var/run/docker.sock"

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type watchState struct {
	Policy               Policy
	Expected             []any
	Egress               []string
	EgressAt             time.Time
	Workspace, Container string
	// BootID is the kernel boot in which a start last completed with an active,
	// verified watcher. Empty means no start completed yet. The restore unit acts
	// whenever it differs from the running kernel boot.
	BootID string `json:",omitempty"`
}

func WatchUnit(network string) string {
	return "stackkit-own-watch-" + identity(network)[:16] + ".service"
}
func watchPath(network string) string { return filepath.Join(watchRoot, Table(network)+".json") }
func docker(ctx context.Context, args ...string) ([]byte, error) {
	return Run(ctx, "docker", append([]string{"--host", localDocker}, args...), nil)
}

// StartWatcher delegates the ONLY start to the kit unit. systemd waits for
// READY after initial verification and start; ExecStopPost kills the exact ID
// even when the watcher is killed, crashes or fails during startup.
func (g *Guard) StartWatcher(ctx context.Context, id string) error {
	return g.startWatcher(ctx, id, false)
}

// startWatcher installs the reboot custody before the watcher starts the
// container: a start that could not survive a reboot does not happen.
// restoring is true only inside the restore unit, which must not stop itself.
func (g *Guard) startWatcher(ctx context.Context, id string, restoring bool) error {
	if !containerID.MatchString(id) || len(g.expected) == 0 || g.workspace == "" {
		return errors.New("own firewall: verified watcher custody required before start")
	}
	stop := StopWatcher
	if restoring {
		stop = stopLiveWatcher
	}
	if err := stop(ctx, g.Policy.Network, false); err != nil {
		return err
	}
	g.container = id
	if err := g.verifyContainer(ctx); err != nil {
		return err
	}
	if err := g.Verify(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(watchRoot, 0700); err != nil {
		return err
	}
	if err := g.saveWatcher(); err != nil {
		return err
	}
	executable, err := kitExecutable()
	if err != nil {
		return err
	}
	if err = g.installRestore(ctx, executable); err != nil {
		return err
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return err
	}
	_, err = Run(ctx, "systemd-run", []string{"--unit=" + WatchUnit(g.Policy.Network), "--collect", "--service-type=notify", "--property=NotifyAccess=main", "--property=Restart=no", "--property=TimeoutStartSec=20s", "--property=TimeoutStopSec=5s", "--property=ExecStopPost=" + dockerPath + " --host " + localDocker + " kill " + id, executable, "--own-egress-watch", watchPath(g.Policy.Network)}, nil)
	if err != nil {
		return err
	}
	if err = g.VerifyWatcher(ctx); err != nil {
		return err
	}
	// Only a start that completed marks this boot as restored; a failed boot
	// start leaves the snapshot unmarked, so the restore unit retries it.
	return g.stampRestore()
}
func (g *Guard) VerifyWatcher(ctx context.Context) error {
	if _, err := Run(ctx, "systemctl", []string{"is-active", "--quiet", WatchUnit(g.Policy.Network)}, nil); err != nil {
		return errors.New("own firewall: kit watcher inactive; application must stay stopped")
	}
	if err := g.verifyContainer(ctx); err != nil {
		return err
	}
	return g.Verify(ctx)
}

// StopWatcher ends the application's lifetime custody: it first disables (and
// stops a running attempt of) the boot-time restore, so a stopped application
// stays stopped across a reboot, then stops the live watcher, whose
// ExecStopPost kills the container. remove also deletes the restore unit and
// both snapshots.
func StopWatcher(ctx context.Context, network string, remove bool) error {
	if err := disableRestore(ctx, network, remove); err != nil {
		return err
	}
	return stopLiveWatcher(ctx, network, remove)
}
func stopLiveWatcher(ctx context.Context, network string, remove bool) error {
	// The exact root-owned state is the unit's ownership marker. No prefix stop.
	if _, err := os.Stat(watchPath(network)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	state, err := Run(ctx, "systemctl", []string{"show", "--property=LoadState", "--value", WatchUnit(network)}, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(state)) != "not-found" {
		if _, err = Run(ctx, "systemctl", []string{"stop", WatchUnit(network)}, nil); err != nil {
			return err
		}
	}
	if remove {
		if err := os.Remove(watchPath(network)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Resume never reapplies rules or adopts fresh host facts. Backup recovery
// must use the exact reviewed apply snapshot and a still-fresh echo.
func Resume(ctx context.Context, network, id, reviewedDigest string) error {
	g, err := loadWatcher(watchPath(network))
	if err != nil {
		return errors.New("own firewall: reviewed watcher snapshot unavailable; reviewed application start required")
	}
	if reviewedDigest == "" || g.Policy.Digest() != reviewedDigest || g.Policy.Network != network || g.container != id {
		return errors.New("own firewall: backup watcher identity differs")
	}
	if err = g.Verify(ctx); err != nil {
		return err
	}
	if err = g.ObserveEgress(ctx, g.workspace); err != nil {
		return err
	}
	return g.StartWatcher(ctx, id)
}
func loadWatcher(path string) (*Guard, error) {
	s, err := loadSnapshot(path, watchPath)
	if err != nil {
		return nil, err
	}
	return s.Guard, nil
}

type snapshot struct {
	*Guard
	bootID string
}

func loadSnapshot(path string, location func(string) string) (snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, err
	}
	var s watchState
	if json.Unmarshal(raw, &s) != nil || path != location(s.Policy.Network) || !containerID.MatchString(s.Container) || len(s.Expected) == 0 || s.Workspace == "" {
		return snapshot{}, errors.New("own firewall: invalid watcher snapshot")
	}
	return snapshot{&Guard{Policy: s.Policy, expected: s.Expected, egress: s.Egress, egressAt: s.EgressAt, workspace: s.Workspace, container: s.Container, run: Run}, s.BootID}, nil
}
func (g *Guard) verifyContainer(ctx context.Context) error {
	raw, err := g.run(ctx, "docker", []string{"--host", localDocker, "inspect", g.container}, nil)
	if err != nil {
		return err
	}
	var entries []struct {
		State           struct{ Running bool }
		NetworkSettings struct {
			Networks map[string]struct{ MacAddress string }
		}
	}
	if json.Unmarshal(raw, &entries) != nil || len(entries) != 1 {
		return errors.New("own firewall: container observation unavailable")
	}
	networks := entries[0].NetworkSettings.Networks
	if len(networks) != 1 {
		return errors.New("own firewall: container attached to a second or missing network")
	}
	endpoint, ok := networks[g.Policy.Network]
	if !ok || ((entries[0].State.Running || endpoint.MacAddress != "") && !strings.EqualFold(endpoint.MacAddress, MAC(g.Policy.Network))) {
		return errors.New("own firewall: container network or MAC differs")
	}
	return nil
}

// WatchDecision is the fail-closed lifetime decision, independent of event
// transport. Missing tables, rule changes, extra networks and stale echoes all
// terminate the unit and hence trigger systemd's exact-container kill.
func WatchDecision(verify error, egressAt, now time.Time) error {
	if verify != nil {
		return verify
	}
	if egressAt.IsZero() || egressAt.After(now) || now.Sub(egressAt) >= EgressMaxAge {
		return errors.New("own firewall: public egress address unknown; last echo older than 90 seconds; stopping application")
	}
	return nil
}
func notifyReady() error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return errors.New("own firewall: watcher requires kit-owned systemd notify unit")
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("READY=1"))
	return err
}
func WatchMain(path string) error {
	g, err := loadWatcher(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// An independent monitor catches changes immediately; the timer also
	// catches dropped monitor events, network attachments and stale discovery.
	monitor := exec.CommandContext(ctx, "nft", "monitor")
	output, err := monitor.StdoutPipe()
	if err != nil {
		return err
	}
	if err = monitor.Start(); err != nil {
		return err
	}
	events := make(chan struct{}, 1)
	dead := make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(output)
		for scan.Scan() {
			select {
			case events <- struct{}{}:
			default:
			}
		}
		dead <- errors.Join(scan.Err(), monitor.Wait(), errors.New("own firewall: nft monitor stopped"))
	}()
	check := func() error {
		c, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		err := g.Verify(c)
		if err == nil {
			err = g.verifyContainer(c)
		}
		if g.Policy.IngressOnly {
			// No destination is granted, so no egress echo can go stale.
			return err
		}
		return WatchDecision(err, g.egressAt, time.Now())
	}
	if err = check(); err != nil {
		return err
	}
	// There is no external compose-up race: this unit owns start and lifetime.
	if _, err = docker(ctx, "start", g.container); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	if err = notifyReady(); err != nil {
		return err
	}
	timer := time.NewTicker(WatchInterval)
	defer timer.Stop()
	type answer struct {
		values []string
		at     time.Time
		err    error
	}
	answers := make(chan answer, 1)
	refreshing := false
	refreshAt := time.Now().Add(EgressRefresh)
	for {
		select {
		case err = <-dead:
			return err
		case a := <-answers:
			refreshing = false
			if a.err != nil {
				fmt.Fprintln(os.Stderr, "own firewall: egress refresh failed; retaining last exclusion until 90s age limit")
			} else {
				c, done := context.WithTimeout(ctx, 2*time.Second)
				err = g.acceptEgress(c, a.values, a.at, g.workspace)
				done()
				if err != nil {
					return err
				}
			}
			if err = check(); err != nil {
				return err
			}
		case <-events:
			if err = check(); err != nil {
				return err
			}
		case <-timer.C:
			if err = check(); err != nil {
				return err
			}
			if !g.Policy.IngressOnly && !time.Now().Before(refreshAt) {
				if !refreshing {
					refreshing = true
					source := Guard{Policy: g.Policy}
					workspace := g.workspace
					go func() { values, e := source.fetchEgress(ctx, workspace); answers <- answer{values, time.Now(), e} }()
				}
				refreshAt = time.Now().Add(EgressRefresh)
			}
		}
	}
}

func VerifyRunning(ctx context.Context, network, id, reviewedDigest string) (err error) {
	defer func() {
		if err != nil && containerID.MatchString(id) {
			stop, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			_, killErr := docker(stop, "kill", id)
			err = errors.Join(err, killErr)
		}
	}()
	g, err := loadWatcher(watchPath(network))
	if err != nil {
		return err
	}
	if reviewedDigest == "" || g.Policy.Digest() != reviewedDigest || g.Policy.Network != network || g.container != id {
		return errors.New("own firewall: runtime differs from signed ruleset")
	}
	return g.VerifyWatcher(ctx)
}

func (g *Guard) saveWatcher() error {
	return g.writeSnapshot(watchRoot, watchPath(g.Policy.Network), "")
}
func (g *Guard) writeSnapshot(root, path, boot string) error {
	raw, err := json.Marshal(watchState{g.Policy, g.expected, g.egress, g.egressAt, g.workspace, g.container, boot})
	if err != nil {
		return err
	}
	return writeAtomic(root, path, raw, 0600)
}
