package hostsecurity

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

const sshdBinary = "sshd"

// sshdObservation is the effective sshd configuration as `sshd -T` prints it:
// lower-case keys, one or more values each.
type sshdObservation struct {
	values map[string][]string
}

func (s sshdObservation) first(key string) string {
	if values := s.values[key]; len(values) > 0 {
		return strings.ToLower(values[0])
	}
	return ""
}

func (s sshdObservation) ports() []int {
	var ports []int
	for _, value := range s.values["port"] {
		if port, err := strconv.Atoi(value); err == nil && port >= 1 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return sortedUniquePorts(ports)
}

func parseSSHD(stdout string) (sshdObservation, error) {
	observation := sshdObservation{values: map[string][]string{}}
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		key = strings.ToLower(key)
		observation.values[key] = append(observation.values[key], strings.TrimSpace(value))
	}
	if len(observation.values) == 0 {
		return sshdObservation{}, errors.New("sshd reported no effective configuration")
	}
	return observation, nil
}

// sshdEffective observes the effective configuration once per run.
func (o *observation) sshdEffective(ctx context.Context) (*sshdObservation, string) {
	if o.sshd != nil || o.sshdFailure != "" {
		return o.sshd, o.sshdFailure
	}
	switch {
	case !o.engine.isRoot():
		o.sshdFailure = rootHint("read the effective sshd configuration (sshd -T)")
	default:
		output, err := o.engine.run(ctx, sshdBinary, "-T")
		switch {
		case errors.Is(err, errToolMissing):
			o.sshdFailure = "sshd is not installed, so there is no ssh login to harden or observe"
		case err != nil:
			o.sshdFailure = "sshd -T could not run: " + err.Error()
		case output.ExitCode != 0:
			o.sshdFailure = "sshd -T failed: " + boundedText(output.Stderr)
		default:
			parsed, parseErr := parseSSHD(output.Stdout)
			if parseErr != nil {
				o.sshdFailure = parseErr.Error()
			} else {
				o.sshd = &parsed
			}
		}
	}
	return o.sshd, o.sshdFailure
}

// sshAbsent reports whether the host has no ssh server to harden. OpenSSH
// missing is a definite observation: there is no ssh login to expose. Another
// ssh server (dropbear) is not inspected, so that case is unknown, reported in
// detail with absent false.
func (o *observation) sshAbsent() (absent bool, detail string) {
	if _, openssh := o.engine.toolPath(sshdBinary); openssh {
		return false, ""
	}
	if _, dropbear := o.engine.toolPath("dropbear"); dropbear {
		return false, "dropbear is installed instead of OpenSSH and is not inspected by this baseline"
	}
	return true, "sshd is not installed, so no ssh login is exposed"
}

func (o *observation) ssh(ctx context.Context) []Control {
	rootRemediation := Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlSSHRootLogin}
	rootExpected := "PermitRootLogin no, or key-only (prohibit-password)"
	if o.options.SiteKind == SiteCloud {
		rootRemediation = Remediation{Capability: RemediationManual, Action: "stackkit apply (the Cloud host-security owner provisions the execution account before it disables root login)"}
		rootExpected = "PermitRootLogin no"
	}
	password := o.control(ControlSSHPassword, "sshd accepts no password or keyboard-interactive logins and no empty passwords",
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlSSHPassword})
	root := o.control(ControlSSHRootLogin, rootExpected, rootRemediation)
	portRemediation := Remediation{Capability: RemediationManual, Action: "align the sshd Port with the firewall baseline, or rerun the firewall repair so it admits the current sshd ports"}
	if o.options.SiteKind == SiteHome {
		portRemediation = Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlFirewall}
	}
	port := o.control(ControlSSHPort, "sshd listens only on ports the firewall baseline keeps open", portRemediation)

	if absent, detail := o.sshAbsent(); absent {
		const observed = "no ssh server is installed"
		return []Control{o.finish(password, StateCompliant, observed, detail), o.finish(root, StateCompliant, observed, detail), o.finish(port, StateCompliant, observed, detail)}
	} else if detail != "" {
		return []Control{o.unknown(password, detail), o.unknown(root, detail), o.unknown(port, detail)}
	}
	settings, failure := o.sshdEffective(ctx)
	if settings == nil {
		return []Control{o.unknown(password, failure), o.unknown(root, failure), o.unknown(port, failure)}
	}
	return []Control{o.sshPassword(password, *settings), o.sshRoot(root, *settings), o.sshPort(ctx, port, *settings)}
}

func (o *observation) sshPassword(c Control, s sshdObservation) Control {
	var offending []string
	for _, key := range []string{"passwordauthentication", "kbdinteractiveauthentication", "challengeresponseauthentication", "permitemptypasswords"} {
		if value := s.first(key); value != "" && value != "no" {
			offending = append(offending, key+" "+value)
		}
	}
	if s.first("passwordauthentication") == "" {
		return o.unknown(c, "sshd -T did not report passwordauthentication")
	}
	if len(offending) > 0 {
		reason := "sshd accepts a login method that is not a public key"
		if blocked, detail := loginKeyProblem(o.accountsWithLoginKeys(&s), o.sessionUser(o.inSSHSession())); blocked {
			// Password logins stay on rather than lock the owner out; the
			// drift is an open finding with the step that closes it.
			c.ReasonCode = ReasonNoAuthorizedKey
			c.Remediation.Action = keyGuidance(o.sessionUser(o.inSSHSession()))
			reason = ReasonNoAuthorizedKey + ": " + detail + "; " + c.Remediation.Action
		}
		return o.finish(c, StateDrifted, strings.Join(offending, ", "), reason)
	}
	return o.finish(c, StateCompliant, "passwordauthentication no, kbdinteractiveauthentication no", "")
}

func (o *observation) sshRoot(c Control, s sshdObservation) Control {
	value := s.first("permitrootlogin")
	switch value {
	case "":
		return o.unknown(c, "sshd -T did not report permitrootlogin")
	case "no":
		return o.finish(c, StateCompliant, "permitrootlogin no", "")
	case "prohibit-password", "without-password", "forced-commands-only":
		if o.options.SiteKind == SiteCloud {
			return o.finish(c, StateDrifted, "permitrootlogin "+value, "the Cloud baseline disables root login entirely")
		}
		return o.finish(c, StateCompliant, "permitrootlogin "+value+" (key-only)", "")
	}
	return o.finish(c, StateDrifted, "permitrootlogin "+value, "root may log in with a password")
}

func (o *observation) sshPort(ctx context.Context, c Control, s sshdObservation) Control {
	ports := s.ports()
	if len(ports) == 0 {
		return o.unknown(c, "sshd -T did not report a listening port")
	}
	observed := "sshd ports " + joinInts(ports)
	switch o.options.SiteKind {
	case SiteCloud:
		// The Cloud ruleset admits tcp/22 only, so any other port is cut off
		// from its own management path.
		for _, port := range ports {
			if port != 22 {
				return o.finish(c, StateDrifted, observed, "the Cloud firewall baseline admits only tcp/22 for ssh")
			}
		}
		return o.finish(c, StateCompliant, observed+"; admitted by the Cloud ruleset", "")
	case SiteHome:
		record := o.recordedFirewall()
		if record == nil {
			return o.unknown(c, "no applied firewall policy record, so the admitted ssh ports cannot be compared; run the firewall repair")
		}
		admitted := sortedUniquePorts(record.Policy.SSHPorts)
		for _, port := range ports {
			if !containsPort(admitted, port) {
				return o.finish(c, StateDrifted, observed+"; firewall admits "+joinInts(admitted), "sshd listens on a port the firewall baseline does not keep open for the management path")
			}
		}
		return o.finish(c, StateCompliant, observed+"; firewall admits "+joinInts(admitted), "")
	}
	_ = ctx
	return o.unknown(c, "site kind is unknown, so the firewall that should admit the ssh port cannot be named")
}
