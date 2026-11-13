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
// The digests qualify the Coolify 4.7.5 files: since 4.4.6 they add the
// expected-stop marker, an optional attempt count that defaults to the
// constants.ssh.max_retries this control sets to 1, host-key mismatch notes
// and output redaction, and keep the synchronous single-attempt transport.
const marketplaceStorePHP = `
error_reporting(0);
$qualified=[
 'app/Actions/Service/StopServiceApplication.php'=>'f4b8a65aea1cc911638abb8c6649769f5b96d84a9451b8370a8278c96a8b0c88',
 'app/Actions/Service/RestartServiceApplication.php'=>'2785d2be60c20aaacf859cbadc856ad9604ac15de74b641e83e03ba6df1b5b27',
 'app/Models/Service.php'=>'47d0d479b6f004c3516f69a4dbc5f2a232fc3a29fd0f04228238c8289256e75a',
 'app/Helpers/SshRetryHandler.php'=>'e045b560e01d1391b9ecbc76fd18a1a6e1f7f0016c3bf933c42d0fe1d90c73da',
 'app/Traits/SshRetryable.php'=>'0a311e0e2c542d77379079e115743127167b53b456ad3bc4da2860ee7cf9e9fa',
 'bootstrap/helpers/remoteProcess.php'=>'b25bac163f6f6c66f60df347acdb799e5e00bd2a0436c60fb9a3fe16a68e3411',
 'app/Jobs/CoolifyTask.php'=>'df64fecb6cbf1b19189e82230ebb22afe6a8eaf2c69d9897f8668c58c12f9c92',
 'app/Actions/CoolifyTask/RunRemoteProcess.php'=>'097ab7edc21f0ba4b42d784b9effcae0aa270f1db508cb4ad760d8bcbfb7cc84',
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
