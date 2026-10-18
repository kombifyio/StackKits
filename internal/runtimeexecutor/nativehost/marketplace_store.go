package nativehost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type marketplaceStoreQuery struct {
	Server    string `json:"server"`
	Service   string `json:"service"`
	Component string `json:"component"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Operation string `json:"operation"`
	Scope     string `json:"scope"`
}

// The only executable PHP is this product-owned native integration. IDs are
// JSON data; the caller cannot supply PHP, shell, credentials or a manager.
const marketplaceStorePHP = `
error_reporting(0);
$qualified=[
 'app/Actions/Service/StopServiceApplication.php'=>'b4eb8d7632b87065988a768aa246720e0cfc2f2e9b99e421a486780d0c264225',
 'app/Actions/Service/RestartServiceApplication.php'=>'4a63827022d7b530d494e9bcaa9127d8dba48f636df657abe359c6d31edd7292',
 'app/Models/Service.php'=>'48bff038d700109c4df35eba6d989dedd2abbb8b33d1e0c36fac181820da72ea',
 'app/Helpers/SshRetryHandler.php'=>'d791da00d2b3e36ce1b7692cc4a241aab818814ce5b764bdb76a915c9ba12fdc',
 'app/Traits/SshRetryable.php'=>'a7c797af05a68ca040e4c633cd33b50518b1acdaae82b16b375224daa411f052',
 'bootstrap/helpers/remoteProcess.php'=>'e529552da9dc8c955d0c879b2fc9754e405ddcdc222109a0be52ab6fa57c37f8',
 'app/Jobs/CoolifyTask.php'=>'fa35f9d1b903f05797999df22215c08feabea3ec20d17c0a16203929d6ed93c4',
 'app/Actions/CoolifyTask/RunRemoteProcess.php'=>'70dac502dd2fa3dfe951f0baf2b6a20e887a1dd475f13a51f3598f4b05cd3118',
 'app/Enums/ProcessStatus.php'=>'5f637d76be5c9d8122f59dcd0e1ebe62632957574aa661d38286b4eea0f505dc'];
foreach($qualified as $file=>$digest){if(hash_file('sha256','/var/www/html/'.$file)!==$digest){exit(74);}}
require '/var/www/html/vendor/autoload.php';
$app=require '/var/www/html/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();

function marketplaceRetainedComponentControl($component,string $action): void {
 config(['queue.default'=>'sync','constants.ssh.max_retries'=>1]);
 app('queue')->setDefaultDriver('sync');
 if (!(app('queue')->connection() instanceof Illuminate\Queue\SyncQueue) || config('constants.ssh.max_retries')!==1) { throw new RuntimeException('native transport not single-attempt synchronous'); }
 if ($action==='stop') { App\Actions\Service\StopServiceApplication::run($component,false,false); }
 elseif ($action==='start') { App\Actions\Service\RestartServiceApplication::run($component); }
 else { throw new RuntimeException('unsupported control'); }
}

try {
 $q=json_decode(base64_decode('__QUERY__'),true,512,JSON_THROW_ON_ERROR);
 if (!in_array($q['scope'],['store','curated'],true)) {throw new RuntimeException('unknown source scope');}
 $matches=collect([App\Models\ServiceApplication::where('uuid',$q['component'])->first(),App\Models\ServiceDatabase::where('uuid',$q['component'])->first()])->filter();
 if ($matches->count()!==1) {throw new RuntimeException('ambiguous component');}
 $a=$matches->first();
 $s=$a->service; $server=$s->destination->server;
 if ($s->uuid!==$q['service'] || $server->uuid!==$q['server'] || $s->server->uuid!==$q['server'] || $a->name!==$q['name'] || $server->isSwarm()) { throw new RuntimeException('source mismatch'); }
 if ($q['scope']==='store' && (!($a instanceof App\Models\ServiceApplication) || $s->applications()->count()!==1 || $s->databases()->count()!==0 || $s->isConfigurationChanged())) {throw new RuntimeException('unsupported or pending Store configuration');}
 if ($q['action']==='inspect') { echo json_encode(['server'=>$server->uuid,'service'=>$s->uuid,'component'=>$a->uuid,'name'=>$a->name]); exit(0); }

 marketplaceRetainedComponentControl($a,$q['action']);
 exit(73);
} catch (Throwable $e) { exit(74); }
`

func marketplaceStoreCommand(q marketplaceStoreQuery) ([]string, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	code := strings.Replace(marketplaceStorePHP, "__QUERY__", base64.StdEncoding.EncodeToString(raw), 1)
	return []string{"php", "-r", code}, nil
}

func marketplaceStoreInstallation(ctx context.Context, workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) (*osMarketplaceOperations, marketplaceCustody, map[string]string, string, error) {
	o, state, err := existingMarketplace(ctx, workspace, d)
	if err != nil {
		return nil, state, nil, "", err
	}
	ids, err := ObserveMarketplaceContainerCustody(ctx, workspace, d)
	if err != nil {
		return nil, state, nil, "", err
	}
	digest, err := resolvedplan.CanonicalSHA256(struct {
		Document      architecturev2renderer.MarketplaceCoolifyDocument
		Owner, Server string
		Containers    map[string]string
	}{d, state.OwnerRef, state.ServerID, ids})
	return o, state, ids, digest, err
}

// ObserveMarketplaceStore validates only sealed local identities. It works
// while the control installation is quiesced; no API/SQL fallback is used.
func ObserveMarketplaceStore(ctx context.Context, workspace string, d architecturev2renderer.MarketplaceCoolifyDocument, selected localbackuppolicy.MarketplaceService) (backupexec.QuiesceContainer, error) {
	_, state, _, installation, err := marketplaceStoreInstallation(ctx, workspace, d)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	if state.SiteRef != selected.SiteRef || state.NodeRef != selected.NodeRef || state.ServerID != selected.ServerUUID || installation != selected.InstallationDigest {
		return backupexec.QuiesceContainer{}, errors.New("Marketplace store installation differs from its Owner-selected node")
	}
	client := docker.NewLocalClient()
	info, err := client.DaemonIdentity(ctx)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	volume, err := client.InspectVolume(ctx, selected.VolumeName)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	expectedPath := filepath.Join(info.DockerRootDir, "volumes", volume.Name, "_data")
	if volume.Driver != "local" || volume.Scope != "local" || len(volume.Options) != 0 || volume.Mountpoint != expectedPath || volume.Mountpoint != filepath.Join(localbackuppolicy.GovernedSource().HostPath, volume.Name, "_data") {
		return backupexec.QuiesceContainer{}, errors.New("Marketplace store requires the exact local named-volume path without driver options")
	}
	root, err := confinedfs.Open(volume.Mountpoint)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	if err := root.Close(); err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	container, err := client.InspectContainer(ctx, selected.ExpectedGeneration.ContainerID)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	labels := container.Config.Labels
	if labels["coolify.serviceUuid"] != selected.ServiceUUID || labels["coolify.service.subUuid"] != selected.ComponentUUID || labels["com.docker.compose.project"] != selected.ServiceUUID || labels["com.docker.compose.service"] != selected.ComponentName {
		return backupexec.QuiesceContainer{}, errors.New("Marketplace store container is outside the selected upstream service/component")
	}
	metadata, err := resolvedplan.CanonicalSHA256(volume)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	observed := selected
	observed.ExpectedGeneration = localbackuppolicy.MarketplaceServiceGeneration{DaemonID: info.ID, ContainerID: container.ID, ImageID: container.Image, VolumeCreatedAt: volume.CreatedAt, VolumeMountpoint: volume.Mountpoint, VolumeMetadataDigest: metadata}
	if err := localbackuppolicy.RequireMarketplaceServiceGeneration(selected, observed); err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	matched := false
	for _, mount := range container.Mounts {
		if mount.RW && mount.Type != "volume" && mount.Type != "tmpfs" {
			return backupexec.QuiesceContainer{}, errors.New("Marketplace store has an unselected persistent bind mount")
		}
		if mount.Type == "volume" && mount.RW {
			if mount.Name != selected.VolumeName || mount.Source != volume.Mountpoint || mount.Destination != selected.ContainerTarget || matched {
				return backupexec.QuiesceContainer{}, errors.New("Marketplace store has an additional or changed persistent writable mount")
			}
			matched = true
		}
	}
	if !matched {
		return backupexec.QuiesceContainer{}, errors.New("Marketplace store has no exact selected writable mount")
	}
	listed, err := client.ListContainers(ctx, true)
	if err != nil {
		return backupexec.QuiesceContainer{}, err
	}
	for _, entry := range listed {
		other, err := client.InspectContainer(ctx, entry.ID)
		if err != nil {
			return backupexec.QuiesceContainer{}, err
		}
		for _, mount := range other.Mounts {
			if other.ID != container.ID && mount.RW && mount.Type == "bind" {
				overlaps, err := marketplaceStoreBindOverlaps(mount.Source, volume.Mountpoint)
				if err != nil {
					return backupexec.QuiesceContainer{}, errors.New("cannot qualify another writable bind mount against the Store source")
				}
				if overlaps {
					return backupexec.QuiesceContainer{}, errors.New("Marketplace store volume has an unselected bind writer")
				}
			}
			if mount.Type == "volume" && mount.Name == selected.VolumeName && mount.RW && other.ID != container.ID {
				return backupexec.QuiesceContainer{}, errors.New("Marketplace store volume has an unselected writer")
			}
		}
	}
	return backupexec.ObserveQuiesceContainer(*container)
}

func marketplaceStoreBindOverlaps(source, volume string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return false, err
	}
	left, err := os.Stat(resolved)
	if err != nil {
		return false, err
	}
	right, err := os.Stat(volume)
	if err != nil {
		return false, err
	}
	if os.SameFile(left, right) {
		return true, nil
	}
	for _, pair := range [][2]string{{resolved, volume}, {volume, resolved}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return false, err
		}
		if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}
