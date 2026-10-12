package nativehost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"golang.org/x/crypto/ssh"
)

type marketplaceSSHObservation struct {
	ApplicationAddress     string `json:"applicationAddress"`
	HostGateway            string `json:"hostGateway"`
	Port                   int    `json:"port"`
	KeyFingerprint         string `json:"keyFingerprint"`
	HostKeyFingerprint     string `json:"hostKeyFingerprint"`
	OwnHostAuthenticated   bool   `json:"ownHostAuthenticated"`
	OutsidePolicyPreserved bool   `json:"outsidePolicyPreserved"`
}

func (o *osMarketplaceOperations) ensureMarketplaceSSH(ctx context.Context, view confinedfs.View, state marketplaceCustody) (marketplaceSSHObservation, error) {
	directory := filepath.Join(o.workspace, filepath.FromSlash(marketplaceDirectory))
	raw, err := o.compose(ctx, directory, nil, "ps", "--quiet", "coolify")
	if err != nil {
		return marketplaceSSHObservation{}, err
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 {
		return marketplaceSSHObservation{}, errors.New("Marketplace SSH requires its single application container")
	}
	raw, err = o.run(ctx, []string{"inspect", "--format", "{{json .NetworkSettings.Networks}}", id}, nil, directory)
	if err != nil {
		return marketplaceSSHObservation{}, err
	}
	var networks map[string]struct {
		IPAddress, Gateway string
		IPPrefixLen        int
	}
	if json.Unmarshal(raw, &networks) != nil {
		return marketplaceSSHObservation{}, errors.New("Marketplace Docker interface observation is invalid")
	}
	network, exists := networks[marketplaceProject+"_marketplace"]
	address, addressErr := netip.ParseAddr(network.IPAddress)
	gateway, gatewayErr := netip.ParseAddr(network.Gateway)
	if !exists || addressErr != nil || gatewayErr != nil || !address.Is4() || !gateway.Is4() || !address.IsPrivate() || !gateway.IsPrivate() || network.IPPrefixLen < 16 || network.IPPrefixLen > 30 || !netip.PrefixFrom(address, network.IPPrefixLen).Contains(gateway) {
		return marketplaceSSHObservation{}, errors.New("Marketplace SSH requires the application's own observed Docker bridge and host gateway")
	}
	private, err := loadMarketplaceSSHPrivateKey(view)
	if err != nil {
		return marketplaceSSHObservation{}, err
	}
	defer clear(private)
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return marketplaceSSHObservation{}, errors.New("Marketplace node SSH key is invalid")
	}
	engine := hostsecurity.Engine{Host: hostsecurity.LocalHost{}}
	policy, err := engine.EnsureMarketplaceRootSSH(ctx, hostsecurity.MarketplaceRootSSH{OwnerRef: state.OwnerRef, Address: address.String(), PublicKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey()))})
	if err != nil {
		return marketplaceSSHObservation{}, err
	}
	hostPublic, err := engine.Host.ReadFile("/etc/ssh/ssh_host_ed25519_key.pub")
	if err != nil {
		return marketplaceSSHObservation{}, errors.New("Marketplace own-host SSH Ed25519 host identity is unavailable")
	}
	hostKey, _, _, rest, err := ssh.ParseAuthorizedKey(hostPublic)
	if err != nil || hostKey.Type() != ssh.KeyAlgoED25519 || len(bytes.TrimSpace(rest)) != 0 {
		return marketplaceSSHObservation{}, errors.New("Marketplace own-host SSH identity is invalid")
	}
	input, _ := json.Marshal(map[string]any{"privateKey": string(private), "gateway": gateway.String(), "port": policy.Port, "serverId": state.ServerID})
	defer clear(input)
	raw, err = o.compose(ctx, directory, input, "exec", "-T", "coolify", "php", "-r", strings.TrimPrefix(marketplaceSSHBindingPHP, "<?php\n"))
	if err != nil {
		return marketplaceSSHObservation{}, err
	}
	var binding struct{ Path, ServerID string }
	if json.Unmarshal(raw, &binding) != nil || binding.ServerID != state.ServerID || !strings.HasPrefix(binding.Path, "/var/www/html/storage/app/ssh/") || filepath.Clean(binding.Path) != binding.Path || strings.ContainsAny(binding.Path, "\r\n\x00") {
		return marketplaceSSHObservation{}, errors.New("Marketplace own-host SSH key binding failed closed")
	}
	knownHost := gateway.String()
	if policy.Port != 22 {
		knownHost = "[" + knownHost + "]:" + strconv.Itoa(policy.Port)
	}
	knownHosts := []byte(knownHost + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostKey))) + "\n")
	// The fixed command proves the real application namespace can authenticate
	// root against the node's independently read host key and Docker daemon.
	raw, err = o.compose(ctx, directory, knownHosts, "exec", "-T", "coolify", "sh", "-ec", `umask 077; cat > /var/www/html/storage/app/ssh/stackkit-known-hosts; exec ssh -o BatchMode=yes -o ConnectTimeout=10 -o IdentityAgent=none -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o GlobalKnownHostsFile=/dev/null -o UserKnownHostsFile=/var/www/html/storage/app/ssh/stackkit-known-hosts -i "$1" -p "$3" "root@$2" 'test "$(id -u)" = 0 && docker info --format "{{.ID}}"'`, "stackkit-marketplace", binding.Path, gateway.String(), strconv.Itoa(policy.Port))
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return marketplaceSSHObservation{}, errors.New("Marketplace application could not authenticate its own root host/Docker connection")
	}
	input, _ = json.Marshal(map[string]string{"serverId": state.ServerID})
	raw, err = o.compose(ctx, directory, input, "exec", "-T", "coolify", "php", "-r", strings.TrimPrefix(marketplaceSSHReadyPHP, "<?php\n"))
	if err != nil || strings.TrimSpace(string(raw)) != "ready" {
		return marketplaceSSHObservation{}, errors.New("Marketplace upstream did not accept its own-host SSH/Docker connection")
	}
	return marketplaceSSHObservation{ApplicationAddress: address.String(), HostGateway: gateway.String(), Port: policy.Port, KeyFingerprint: policy.KeyFingerprint, HostKeyFingerprint: ssh.FingerprintSHA256(hostKey), OwnHostAuthenticated: true, OutsidePolicyPreserved: policy.OutsidePolicyPreserved}, nil
}

func loadMarketplaceSSHPrivateKey(view confinedfs.View) ([]byte, error) {
	file, err := view.Open("ssh-private-key")
	if err == nil {
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("Marketplace SSH private custody must remain private")
		}
		raw, err := io.ReadAll(io.LimitReader(file, 8193))
		if err != nil || len(raw) > 8192 {
			clear(raw)
			return nil, errors.New("Marketplace SSH key exceeds its custody bound")
		}
		key, err := ssh.ParseRawPrivateKey(raw)
		if err != nil {
			clear(raw)
			return nil, errors.New("Marketplace SSH custody is invalid")
		}
		if _, ok := key.(*ed25519.PrivateKey); !ok {
			clear(raw)
			return nil, errors.New("Marketplace SSH custody requires its Ed25519 key")
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	defer clear(private)
	block, err := ssh.MarshalPrivateKey(private, "StackKits Marketplace own host")
	if err != nil {
		return nil, err
	}
	raw := pem.EncodeToMemory(block)
	if _, err := view.WriteAtomic0600("ssh-private-key", raw); err != nil {
		clear(raw)
		return nil, err
	}
	return raw, nil
}

const marketplaceSSHBindingPHP = `<?php
require '/var/www/html/vendor/autoload.php';
$app=require '/var/www/html/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
try {
 $input=json_decode(stream_get_contents(STDIN),true,32,JSON_THROW_ON_ERROR);
 $server=App\Models\Server::findOrFail(0);
 if($server->uuid!==$input['serverId'] || $server->team_id!==0 || !in_array($server->ip,['host.docker.internal','localhost','127.0.0.1',$input['gateway']],true)){throw new RuntimeException('Another server target');}
 $keys=App\Models\PrivateKey::where('team_id',0)->where('name','StackKits Marketplace own host')->get();
 if($keys->count()>1){throw new RuntimeException('Ambiguous node SSH key');}
 $key=$keys->first();
 if($key && trim($key->private_key)!==trim($input['privateKey'])){throw new RuntimeException('Another node SSH key');}
 if(!$key){$key=App\Models\PrivateKey::create(['team_id'=>0,'name'=>'StackKits Marketplace own host','description'=>'Node-custodied Docker-bridge-scoped root key','private_key'=>$input['privateKey']]);}
 $key->storeInFileSystem();
 $server->ip=$input['gateway'];$server->port=$input['port'];$server->user='root';$server->private_key_id=$key->id;$server->save();
 if($server->fresh()->private_key_id!==$key->id){throw new RuntimeException('Node SSH binding missing');}
 echo json_encode(['path'=>$key->getKeyLocation(),'serverID'=>$server->uuid],JSON_THROW_ON_ERROR);
} catch(Throwable $e){fwrite(STDERR,'Marketplace own-host SSH binding failed ('.get_class($e).')');exit(1);}
`

const marketplaceSSHReadyPHP = `<?php
require '/var/www/html/vendor/autoload.php';
$app=require '/var/www/html/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
try {
 $input=json_decode(stream_get_contents(STDIN),true,32,JSON_THROW_ON_ERROR);
 $server=App\Models\Server::findOrFail(0);
 if($server->uuid!==$input['serverId']){throw new RuntimeException('Another server');}
 $check=$server->validateConnection(true);
 if(!($check['uptime']??false) || !$server->validateDockerEngine(true)){throw new RuntimeException('Own-host SSH/Docker validation failed');}
 $settings=$server->settings->fresh();
 if(!$settings->is_reachable || !$settings->is_usable){throw new RuntimeException('Upstream server is not ready');}
 echo 'ready';
} catch(Throwable $e){fwrite(STDERR,'Marketplace upstream SSH readiness failed ('.get_class($e).')');exit(1);}
`
