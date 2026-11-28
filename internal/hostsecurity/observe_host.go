package hostsecurity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

const ControlSSHObserve = "ssh.observe_isolation"

const SSHObserveUser = observeUser

type ObserveSSH struct {
	Profile            string `json:"profile"`
	Principal          string `json:"principal"`
	ConfiguredPort     int    `json:"configured_port"`
	HostPublicKey      string `json:"host_public_key"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
}

var observeLauncher = []byte("#!" + observeCLI + "\n")

var observeSSHDSettings = [][2]string{
	{"AuthenticationMethods", "publickey"},
	{"PubkeyAuthentication", "yes"},
	{"PubkeyAcceptedAlgorithms", ssh.CertAlgoED25519v01},
	{"TrustedUserCAKeys", SSHTrustedUserCAKeysPath},
	{"AuthorizedPrincipalsFile", "none"},
	{"AuthorizedPrincipalsCommand", observeCLI + " " + observeAuthorize + " %t %k"},
	{"AuthorizedPrincipalsCommandUser", observeUser},
	{"AuthorizedKeysFile", "none"},
	{"AuthorizedKeysCommand", "none"},
	{"PasswordAuthentication", "no"},
	{"KbdInteractiveAuthentication", "no"},
	{"HostbasedAuthentication", "no"},
	{"DisableForwarding", "yes"},
	{"PermitTunnel", "no"},
	{"PermitTTY", "no"},
	{"PermitUserRC", "no"},
	{"ExposeAuthInfo", "yes"},
	{"MaxSessions", "1"},
	{"ForceCommand", observeCommand},
}

func renderObserveSSHD() []byte {
	var config strings.Builder
	config.WriteString(managedSSHHeader + "\nMatch User " + observeUser + "\n")
	for _, setting := range observeSSHDSettings {
		config.WriteString("    " + setting[0] + " " + setting[1] + "\n")
	}
	return []byte(config.String())
}

func (e Engine) stageObserve(ctx context.Context, files *sshFiles, principal string) (stageErr error) {
	if err := files.write(observeShell, observeLauncher, 0o755); err != nil {
		return err
	}
	files.changes = append(files.changes, "ensure locked observe account with no supplementary groups", "run the observe sandbox refusal probe")
	if files.apply {
		account, err := e.run(ctx, "getent", "passwd", observeUser)
		if err != nil {
			return err
		}
		if account.ExitCode == 2 {
			result, err := e.run(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", observeHome, "--shell", observeShell, observeUser)
			if err != nil || result.ExitCode != 0 {
				return errors.New("could not create the locked observe account")
			}
		}
		if err := e.observeAccount(ctx); err != nil {
			return err
		}
		if err := e.observeRefusals(ctx); err != nil {
			return err
		}
	}
	previous, err := e.Host.ReadFile(observeDropIn)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := files.write(observeDropIn, renderObserveSSHD(), 0o600); err != nil {
		return err
	}
	if files.apply {
		defer func() {
			if stageErr == nil {
				return
			}
			var restoreErr error
			if previous == nil {
				restoreErr = e.Host.Remove(observeDropIn)
			} else {
				restoreErr = e.Host.WriteFile(observeDropIn, previous, 0o600)
			}
			stageErr = errors.Join(stageErr, restoreErr, e.reloadSSHD(ctx))
		}()
		if err := e.testSSHD(ctx); err != nil {
			return err
		}
		settings, err := e.observeEffective(ctx)
		if err != nil {
			return err
		}
		if _, err := e.observeEndpoint(ctx, settings, principal); err != nil {
			return err
		}
		if err := e.reloadSSHD(ctx); err != nil {
			return err
		}
	}
	return files.write(observePrincipalPath, []byte(principal+"\n"), 0o644)
}

func (e Engine) observeAccount(ctx context.Context) error {
	for _, path := range []string{observeHome, "/etc/ssh/sshrc"} {
		if _, err := e.Host.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("observe requires %s to be absent", path)
		}
	}
	for _, path := range []string{observeCLI, filepath.Dir(observeCLI), observeShell, filepath.Dir(observeShell), SSHAccessDir, SSHTrustedUserCAKeysPath,
		"/usr/bin/bwrap", "/usr/bin/setpriv", "/usr/bin/prlimit", "/bin/busybox"} {
		info, err := e.run(ctx, "stat", "-Lc", "%u %a", path)
		metadata := strings.Fields(info.Stdout)
		if err != nil || info.ExitCode != 0 || len(metadata) != 2 || metadata[0] != "0" {
			return fmt.Errorf("observe requires root-owned %s", path)
		}
		mode, err := strconv.ParseUint(metadata[1], 8, 32)
		if err != nil || mode&0o6022 != 0 {
			return fmt.Errorf("observe requires %s not to be set-ID or writable by other users", path)
		}
	}
	account, err := e.run(ctx, "getent", "passwd", observeUser)
	fields := strings.Split(strings.TrimSpace(account.Stdout), ":")
	if err != nil || account.ExitCode != 0 || len(fields) != 7 || fields[0] != observeUser || fields[5] != observeHome || fields[6] != observeShell {
		return errors.New("the observe account is not configured with the managed launcher and no home")
	}
	uid, uidErr := strconv.ParseUint(fields[2], 10, 32)
	gid, gidErr := strconv.ParseUint(fields[3], 10, 32)
	if uidErr != nil || gidErr != nil || uid == 0 || gid == 0 {
		return errors.New("the observe account must be unprivileged")
	}
	group, err := e.run(ctx, "getent", "group", observeUser)
	groupFields := strings.Split(strings.TrimSpace(group.Stdout), ":")
	groups, groupsErr := e.run(ctx, "id", "-G", observeUser)
	if err != nil || group.ExitCode != 0 || len(groupFields) != 4 || groupFields[0] != observeUser || groupFields[2] != fields[3] || groupFields[3] != "" ||
		groupsErr != nil || groups.ExitCode != 0 || strings.TrimSpace(groups.Stdout) != fields[3] {
		return errors.New("the observe account must have an empty private group and no supplementary group")
	}
	shadow, err := e.run(ctx, "getent", "shadow", observeUser)
	shadowFields := strings.SplitN(strings.TrimSpace(shadow.Stdout), ":", 3)
	if err != nil || shadow.ExitCode != 0 || len(shadowFields) != 3 || shadowFields[0] != observeUser ||
		(!strings.HasPrefix(shadowFields[1], "!") && !strings.HasPrefix(shadowFields[1], "*")) {
		return errors.New("the observe account must not have a usable password")
	}
	return nil
}

func (e Engine) observeEffective(ctx context.Context) (sshdObservation, error) {
	if err := e.observeMatchOrder(); err != nil {
		return sshdObservation{}, err
	}
	var observed sshdObservation
	for _, address := range []string{"127.0.0.1", "192.0.2.1", "2001:db8::1"} {
		output, err := e.run(ctx, sshdBinary, "-T", "-C", "user="+observeUser+",host=stackkit-observe,addr="+address)
		if err != nil || output.ExitCode != 0 {
			return sshdObservation{}, errors.New("could not read effective sshd policy for the observe account")
		}
		observed, err = parseSSHD(output.Stdout)
		if err != nil {
			return sshdObservation{}, err
		}
		for _, setting := range observeSSHDSettings {
			if !slices.Equal(observed.values[strings.ToLower(setting[0])], []string{setting[1]}) {
				return sshdObservation{}, fmt.Errorf("sshd overrides observe setting %s", setting[0])
			}
		}
		if observed.first("usepam") != "yes" {
			return sshdObservation{}, errors.New("observe requires PAM for certificate login on the password-locked account")
		}
		if observed.first("permituserenvironment") != "no" || len(observed.values["setenv"]) != 0 {
			return sshdObservation{}, errors.New("sshd environment injection is incompatible with the observe profile")
		}
		for _, pattern := range strings.Fields(strings.Join(observed.values["acceptenv"], " ")) {
			switch pattern {
			case "LANG", "LC_*", "COLORTERM", "NO_COLOR":
			default:
				return sshdObservation{}, errors.New("sshd accepts environment variables outside the observe profile")
			}
		}
		if err := observeAdmission(observed); err != nil {
			return sshdObservation{}, err
		}
	}
	return observed, nil
}

func observeAdmission(observed sshdObservation) error {
	for _, directive := range []string{"allowusers", "allowgroups"} {
		patterns := strings.Fields(strings.Join(observed.values[directive], " "))
		if len(patterns) != 0 && !slices.Contains(patterns, observeUser) && !slices.Contains(patterns, "*") {
			return fmt.Errorf("sshd %s does not explicitly admit observe", directive)
		}
	}
	for _, directive := range []string{"denyusers", "denygroups"} {
		for _, pattern := range strings.Fields(strings.Join(observed.values[directive], " ")) {
			if pattern == observeUser || strings.ContainsAny(pattern, "*?!@") {
				return fmt.Errorf("sshd %s may deny observe", directive)
			}
		}
	}
	return nil
}

func (e Engine) observeMatchOrder() error {
	config, err := e.Host.ReadFile("/etc/ssh/sshd_config")
	if err != nil {
		return err
	}
	directory, file := filepath.Split(observeDropIn)
	failure := errors.New("observe must be the first Match policy: include the managed sshd drop-in directory globally, before any other Include or Match")
	for _, line := range managedLines(config) {
		directive, _, _ := strings.Cut(line, "#")
		parts := strings.Fields(directive)
		if len(parts) == 0 {
			continue
		}
		keyword := strings.ToLower(parts[0])
		if keyword == "match" || strings.HasPrefix(keyword, "match=") || strings.HasPrefix(keyword, "include=") {
			return failure
		}
		if keyword != "include" {
			continue
		}
		if len(parts) != 2 || parts[1] != directory+"*.conf" {
			return failure
		}
		names, err := e.Host.ReadDirNames(filepath.Dir(observeDropIn))
		if err != nil {
			return err
		}
		for _, name := range names {
			if !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".conf") && name < file {
				return failure
			}
		}
		return nil
	}
	return failure
}

func (e Engine) observeRefusals(ctx context.Context) error {
	output, err := e.run(ctx, "runuser", "-u", observeUser, "--", observeShell, "--probe")
	if err != nil || output.ExitCode != 0 || output.Stdout != "write refused\nprotected read refused\nonward connection refused\n" {
		return errors.New("observe sandbox did not prove the write, protected-read and onward-connection refusals")
	}
	return nil
}

func (e Engine) observeEndpoint(ctx context.Context, settings sshdObservation, principal string) (*ObserveSSH, error) {
	ports := settings.ports()
	if len(ports) != 1 {
		return nil, errors.New("observe needs a single configured sshd port")
	}
	if !slices.Contains(strings.Split(settings.first("hostkeyalgorithms"), ","), ssh.KeyAlgoED25519) {
		return nil, errors.New("observe needs sshd to offer raw Ed25519 host keys")
	}
	var publicKey ssh.PublicKey
	for _, path := range settings.values["hostkey"] {
		output, err := e.run(ctx, "ssh-keygen", "-y", "-f", path)
		if err != nil || output.ExitCode != 0 {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(output.Stdout))
		if err != nil || key.Type() != ssh.KeyAlgoED25519 {
			continue
		}
		if publicKey != nil {
			return nil, errors.New("observe requires exactly one configured Ed25519 host key")
		}
		publicKey = key
	}
	if publicKey == nil {
		return nil, errors.New("observe needs a readable configured Ed25519 host key")
	}
	return &ObserveSSH{
		Profile: observeProfile, Principal: principal, ConfiguredPort: ports[0],
		HostPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))), HostKeyFingerprint: ssh.FingerprintSHA256(publicKey),
	}, nil
}

func (o *observation) sshObserve(ctx context.Context) (Control, *ObserveSSH) {
	state := o.options.SSHAccess
	if state == nil {
		return Control{}, nil
	}
	c := o.control(ControlSSHObserve, "only the delivered observe principal reaches the isolated shell",
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlSSHObserve})
	installed, err := o.engine.Host.ReadFile(observePrincipalPath)
	if state.Failure != "" {
		if errors.Is(err, fs.ErrNotExist) {
			return Control{}, nil
		}
		return o.unknown(c, "the recorded SSH access cannot be read"), nil
	}
	principal := observePrincipal(state.Desired.Principals)
	if principal == "" && errors.Is(err, fs.ErrNotExist) {
		return Control{}, nil
	}
	if err != nil || principal == "" || !bytes.Equal(installed, []byte(principal+"\n")) {
		return o.finish(c, StateDrifted, "observe grant differs from custody", "the host must authorize exactly the delivered principal"), nil
	}
	for _, expected := range []struct {
		path string
		data []byte
	}{{observeShell, observeLauncher}, {observeDropIn, renderObserveSSHD()}} {
		actual, err := o.engine.Host.ReadFile(expected.path)
		if err != nil || !bytes.Equal(actual, expected.data) {
			return o.finish(c, StateDrifted, "observe launcher or sshd policy differs", expected.path), nil
		}
	}
	if !o.engine.isRoot() {
		return o.unknown(c, "verify observe from the managed host as root"), nil
	}
	endpoint, err := o.engine.proveObserve(ctx, principal)
	if err != nil {
		return o.finish(c, StateDrifted, "observe profile is not proven", err.Error()), nil
	}
	return o.finish(c, StateCompliant, "write, protected read and onward connection refused", ""), endpoint
}

func (e Engine) proveObserve(ctx context.Context, principal string) (*ObserveSSH, error) {
	if err := e.observeAccount(ctx); err != nil {
		return nil, err
	}
	settings, err := e.observeEffective(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.observeRefusals(ctx); err != nil {
		return nil, err
	}
	return e.observeEndpoint(ctx, settings, principal)
}

func observePrincipal(principals map[string][]string) string {
	names := principals[observeUser]
	if len(names) == 1 && observePrincipalPattern.MatchString(names[0]) {
		return names[0]
	}
	return ""
}
