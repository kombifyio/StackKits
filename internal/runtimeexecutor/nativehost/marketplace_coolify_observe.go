package nativehost

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// existingMarketplace reads established node custody without bootstrapping,
// adopting containers, issuing credentials, or replaying installation.
func existingMarketplace(ctx context.Context, workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) (*osMarketplaceOperations, marketplaceCustody, error) {
	if ctx == nil {
		return nil, marketplaceCustody{}, errors.New("Marketplace observation requires its operation context")
	}
	if err := architecturev2renderer.ValidateMarketplaceCoolifyDocument(d); err != nil {
		return nil, marketplaceCustody{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil || owner.Binding.SiteRef != d.SiteRef || owner.Binding.NodeRef != d.NodeRef {
		return nil, marketplaceCustody{}, errors.New("Marketplace observation requires the exact established node owner")
	}
	if err := requireMarketplaceKitContext(workspace, d); err != nil {
		return nil, marketplaceCustody{}, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, marketplaceCustody{}, err
	}
	defer root.Close()
	view, err := root.View(marketplaceDirectory)
	if err != nil {
		return nil, marketplaceCustody{}, err
	}
	file, err := view.Open("custody.json")
	if err != nil {
		return nil, marketplaceCustody{}, errors.New("Marketplace observation refuses an installation without node custody")
	}
	if err := file.Close(); err != nil {
		return nil, marketplaceCustody{}, err
	}
	o := &osMarketplaceOperations{workspace: root.Name()}
	state, err := o.loadMarketplaceCustody(ctx, view, owner.OwnerRef, d)
	if err != nil {
		return nil, state, err
	}
	if state.ServerID == "" || state.APIToken == "" {
		return nil, state, errors.New("Marketplace observation requires completed private API and server custody")
	}
	return o, state, nil
}

// ObserveMarketplaceContainerCustody admits the installed container IDs for
// the central backup quiescer. Labels are checked against the signed recipe
// and private node installation; they never select projects or sources.
func ObserveMarketplaceContainerCustody(ctx context.Context, workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) (map[string]string, error) {
	o, _, err := existingMarketplace(ctx, workspace, d)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(o.workspace, filepath.FromSlash(marketplaceDirectory))
	ids := make(map[string]string, len(d.Components))
	for _, component := range d.Components {
		raw, err := o.compose(ctx, directory, nil, "ps", "--all", "--quiet", component.ID)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSpace(string(raw))
		if _, err := hex.DecodeString(id); err != nil || len(id) != 64 {
			return nil, errors.New("Marketplace component has no unique installed container identity")
		}
		raw, err = o.run(ctx, []string{"inspect", id}, nil, directory)
		if err != nil {
			return nil, err
		}
		var containers []struct {
			ID     string `json:"Id"`
			Config struct {
				Image  string            `json:"Image"`
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
			Mounts []struct {
				Type, Name, Destination string
				RW                      bool
			} `json:"Mounts"`
		}
		if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 || containers[0].ID != id {
			return nil, errors.New("Marketplace container inspection is ambiguous")
		}
		container := containers[0]
		labels := container.Config.Labels
		if container.Config.Image != component.ImageRef+"@"+component.ImageDigest || labels["com.docker.compose.project"] != marketplaceProject || labels["com.docker.compose.service"] != component.ID || labels["com.docker.compose.project.working_dir"] != directory || labels["com.docker.compose.project.config_files"] != filepath.Join(directory, "compose.yaml") {
			return nil, errors.New("Marketplace container differs from its private installation and pinned component")
		}
		seen := map[string]bool{}
		for _, mount := range container.Mounts {
			if mount.Type != "volume" {
				continue
			}
			prefix := marketplaceProject + "_marketplace-"
			if !strings.HasPrefix(mount.Name, prefix) {
				return nil, errors.New("Marketplace container acquired an unowned persistent volume")
			}
			volume := strings.TrimPrefix(mount.Name, prefix)
			target, admitted := localbackuppolicy.MarketplaceVolumeTarget(component.ID, volume)
			if !admitted || seen[volume] || mount.Destination != target || !mount.RW {
				return nil, errors.New("Marketplace container differs from its selected persistent volume layout")
			}
			seen[volume] = true
		}
		wanted := 1
		if component.ID == "coolify" {
			wanted = 6
		}
		if len(seen) != wanted {
			return nil, errors.New("Marketplace container is missing a selected persistent volume")
		}
		ids[component.ID] = id
	}
	return ids, nil
}

// VerifyMarketplaceReady is a read-only restore postcondition. It observes
// the held node installation and private API; it does not repair state by
// running Apply or silently change a restored database.
func VerifyMarketplaceReady(ctx context.Context, workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) error {
	if _, err := ObserveMarketplaceContainerCustody(ctx, workspace, d); err != nil {
		return err
	}
	o, state, err := existingMarketplace(ctx, workspace, d)
	if err != nil {
		return err
	}
	directory := filepath.Join(o.workspace, filepath.FromSlash(marketplaceDirectory))
	endpoint, err := o.endpoint(ctx, directory)
	if err != nil {
		return err
	}
	if err := waitMarketplaceHTTP(ctx, endpoint); err != nil {
		return err
	}
	if err := verifyMarketplaceAPI(ctx, endpoint, state.APIToken); err != nil {
		return err
	}
	if d.Route.ID != "" {
		if err := o.verifyPrivateUI(ctx, d); err != nil {
			return err
		}
	}
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil {
		return err
	}
	php := `require '/var/www/html/vendor/autoload.php';$app=require '/var/www/html/bootstrap/app.php';$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();$i=App\Models\InstanceSettings::get();$u=App\Models\User::find(0);$s=App\Models\Server::find(0);if(!$u||$u->email!==getenv('STACKKIT_EXPECT_OWNER')||!$s||$s->uuid!==getenv('STACKKIT_EXPECT_SERVER')||$s->proxy->get('type')!=='NONE'||$i->is_registration_enabled||$i->is_auto_update_enabled){throw new Exception('Marketplace restored identity or instance policy differs');}echo json_encode(['ready'=>true]);`
	raw, err := o.compose(ctx, directory, nil, "exec", "-T", "-e", "STACKKIT_EXPECT_OWNER="+owner.PocketID.Email, "-e", "STACKKIT_EXPECT_SERVER="+state.ServerID, "coolify", "php", "-r", php)
	if err != nil {
		return fmt.Errorf("observe restored Marketplace identity: %w", err)
	}
	var result struct {
		Ready bool `json:"ready"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.Ready {
		return errors.New("Marketplace restored identity observation is invalid")
	}
	return nil
}
