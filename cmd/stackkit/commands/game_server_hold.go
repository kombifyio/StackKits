package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

// Wings-owned game containers are outside the Compose graph the snapshot owner
// quiesces (ADR-0043). A game server that keeps running while its world is
// copied gives a crash-consistent world at best, and one that keeps running
// while a restore replaces its world writes the old state back. The hold
// stops every running game server with the game's own stop command (which
// saves the world), runs the operation, and starts exactly those servers
// again. A journal lets the next hold resume servers an interrupted one left
// stopped.

const gameServerHoldJournal = ".stackkit/backups/game-server-hold.json"

type gameServerHoldRecord struct {
	Servers   []string  `json:"servers"`
	StartedAt time.Time `json:"startedAt"`
}

// withGameServersHeld runs operation with the owner's running game servers
// stopped. Without an applied Game workload it runs operation directly. When
// required is false, a Panel that cannot be reached degrades to a warning so a
// crash-consistent backup still happens; restore passes required=true.
func withGameServersHeld(ctx context.Context, workspace string, required bool, operation func() error) error {
	authority, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
	if err != nil || !planSelectsWorkload(authority, "game") {
		return operation()
	}
	deployment, err := nativeAppliedWorkloadDeployment(authority, "game")
	if err != nil {
		return holdUnavailable(required, err, operation)
	}
	_, clientKey, err := pterodactylCustodyKeys(workspace, deployment)
	if err != nil {
		return holdUnavailable(required, err, operation)
	}
	journalPath := filepath.Join(workspace, filepath.FromSlash(gameServerHoldJournal))
	panel := func(use func(client *http.Client, baseURL string) error) error {
		return runtimeexecutorlocal.WithStandaloneComposeHTTP(ctx, workspace, deployment, use)
	}
	held := readGameServerHold(journalPath)
	stopErr := panel(func(client *http.Client, baseURL string) error {
		states, err := appsetup.OwnedGameServers(ctx, client, baseURL, clientKey)
		if err != nil {
			return err
		}
		for identifier, state := range states {
			if state != "offline" && state != "" && !slices.Contains(held, identifier) {
				held = append(held, identifier)
			}
		}
		sort.Strings(held)
		if err := writeGameServerHold(journalPath, held); err != nil {
			return err
		}
		for _, identifier := range held {
			if states[identifier] == "offline" {
				continue
			}
			if err := appsetup.StopGameServer(ctx, client, baseURL, clientKey, identifier, 3*time.Minute); err != nil {
				return err
			}
		}
		return nil
	})
	resume := func() error {
		if len(held) == 0 {
			_ = os.Remove(journalPath)
			return nil
		}
		// The operation may have recreated the Panel; reconnect and give it
		// time to boot before the start signals.
		deadline := time.Now().Add(4 * time.Minute)
		for {
			pending := []string{}
			err := panel(func(client *http.Client, baseURL string) error {
				var errs []error
				for _, identifier := range held {
					if err := appsetup.StartGameServer(context.WithoutCancel(ctx), client, baseURL, clientKey, identifier); err != nil {
						pending = append(pending, identifier)
						errs = append(errs, err)
					}
				}
				return errors.Join(errs...)
			})
			if err == nil {
				_ = os.Remove(journalPath)
				printInfo("Started %d game server(s) again", len(held))
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("restart game servers after the operation (the next backup or restore retries them): %w", err)
			}
			if len(pending) > 0 {
				held = pending
			}
			time.Sleep(10 * time.Second)
		}
	}
	if stopErr != nil {
		if len(held) == 0 {
			return holdUnavailable(required, stopErr, operation)
		}
		return errors.Join(fmt.Errorf("stop game servers before the operation: %w", stopErr), resume())
	}
	if len(held) > 0 {
		printInfo("Stopped %d game server(s) so their worlds are saved consistently", len(held))
	}
	return errors.Join(operation(), resume())
}

func holdUnavailable(required bool, cause error, operation func() error) error {
	if required {
		return fmt.Errorf("game servers must be stopped first, but the game Panel is not usable: %w", cause)
	}
	printWarning("Game servers could not be stopped (%v); world copies in this snapshot are crash-consistent", cause)
	return operation()
}

func planSelectsWorkload(authority nativeV2AppliedAuthority, workload string) bool {
	for _, target := range authority.Plan.ApplyRequirements().RuntimeInstances {
		if target.WorkloadRef == workload {
			return true
		}
	}
	return false
}

func readGameServerHold(path string) []string {
	var record gameServerHoldRecord
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &record) != nil {
		return nil
	}
	return record.Servers
}

func writeGameServerHold(path string, servers []string) error {
	raw, err := json.Marshal(gameServerHoldRecord{Servers: servers, StartedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
