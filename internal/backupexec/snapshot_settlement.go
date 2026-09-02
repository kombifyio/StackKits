package backupexec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

type snapshotSettlementClient interface {
	ListContainers(context.Context, bool) ([]docker.ContainerInfo, error)
	InspectContainer(context.Context, string) (*docker.ContainerInfo, error)
	InspectNetwork(context.Context, string) (*docker.NetworkInfo, error)
	StopContainer(context.Context, string) error
	StartContainer(context.Context, string) error
}

// NewDockerV2SnapshotSettler ends a possibly orphaned docker-exec snapshot
// before its writers may resume. Killing the docker CLI alone cannot prove
// that the daemon-side Kopia process stopped. The dedicated idle backup
// container is restarted only on this explicit mutation/recovery path.
func NewDockerV2SnapshotSettler(source localbackuppolicy.Source) func(context.Context) error {
	return dockerV2SnapshotSettler(func(timeout time.Duration) snapshotSettlementClient {
		return docker.NewLocalClient(docker.WithTimeout(timeout))
	}, source)
}

func dockerV2SnapshotSettler(newClient func(time.Duration) snapshotSettlementClient, source localbackuppolicy.Source) func(context.Context) error {
	return func(ctx context.Context) error {
		if ctx == nil || newClient == nil {
			return errors.New("snapshot settlement requires a context and local Docker client")
		}
		bounded, cancel := context.WithTimeout(ctx, QuickOperationTimeout)
		defer cancel()
		client := newClient(dockerTimeout(bounded, QuickOperationTimeout))
		listed, err := client.ListContainers(bounded, true)
		if err != nil {
			return fmt.Errorf("list snapshot runtime for settlement: %w", err)
		}
		var target *docker.ContainerInfo
		for _, item := range listed {
			current, err := client.InspectContainer(bounded, item.ID)
			if err != nil {
				return fmt.Errorf("inspect snapshot settlement candidate: %w", err)
			}
			if current == nil || current.Config.Labels["com.docker.compose.project"] != v2ComposeProject || current.Config.Labels["com.docker.compose.service"] != v2ComposeService {
				continue
			}
			if target != nil {
				return errors.New("snapshot settlement requires exactly one governed Kopia container")
			}
			target = current
		}
		if target == nil || target.ID == "" {
			return errors.New("snapshot settlement has no exact governed Kopia container")
		}
		network, err := client.InspectNetwork(bounded, v2NetworkName)
		if err != nil {
			return fmt.Errorf("inspect snapshot settlement network: %w", err)
		}
		if err := validateDockerV2RuntimeState(target, network, source, true); err != nil {
			return fmt.Errorf("snapshot settlement runtime differs from its policy: %w", err)
		}
		networkID := network.ID
		if networkID == "" || target.NetworkSettings.Networks[v2NetworkName].NetworkID != networkID {
			return errors.New("snapshot settlement network identity differs from the configured container network")
		}
		if target.State.Paused || target.State.Restarting {
			return errors.New("snapshot settlement refuses a paused or restarting Kopia container")
		}
		if target.State.Running {
			if err := client.StopContainer(bounded, target.ID); err != nil {
				return fmt.Errorf("stop interrupted Kopia snapshot: %w", err)
			}
		}
		stopped, err := client.InspectContainer(bounded, target.ID)
		if err != nil {
			return fmt.Errorf("verify stopped Kopia snapshot: %w", err)
		}
		if stopped == nil || stopped.ID != target.ID || stopped.State.Status != "exited" || stopped.State.Running || stopped.State.Paused || stopped.State.Restarting {
			return errors.New("Kopia snapshot process has not reached a verified stopped state")
		}
		if err := client.StartContainer(bounded, target.ID); err != nil {
			return fmt.Errorf("restart settled Kopia runtime: %w", err)
		}
		for {
			current, err := client.InspectContainer(bounded, target.ID)
			if err != nil {
				return fmt.Errorf("verify restarted Kopia runtime: %w", err)
			}
			if current == nil || current.ID != target.ID {
				return errors.New("restarted Kopia runtime identity differs from the settled container")
			}
			network, err := client.InspectNetwork(bounded, v2NetworkName)
			if err != nil {
				return fmt.Errorf("verify restarted Kopia network: %w", err)
			}
			if network == nil || network.ID != networkID || current.NetworkSettings.Networks[v2NetworkName].NetworkID != networkID {
				return errors.New("restarted Kopia network identity differs from the settled network")
			}
			if err := validateDockerV2RuntimeForSource(current, network, source); err != nil {
				return err
			}
			if current.State.Running && !current.State.Paused && !current.State.Restarting && current.State.Health != nil && current.State.Health.Status == "healthy" {
				return nil
			}
			select {
			case <-bounded.Done():
				return fmt.Errorf("settled Kopia runtime did not become healthy: %w", bounded.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}
