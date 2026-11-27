package exposureproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// VantageKind says where a probe ran relative to the owner's network. Only an
// external vantage can prove how an address looks from the public internet.
type VantageKind string

const (
	VantageExternal VantageKind = "external"
	VantageLAN      VantageKind = "lan"
	VantageHost     VantageKind = "host-local"
)

// Vantage is the declared place a prober runs from and the address families it
// can speak from there. Both are asserted by the operator of the prober and
// recorded in the report; the evaluator trusts nothing beyond them.
type Vantage struct {
	ID       string      `json:"id"`
	Kind     VantageKind `json:"kind"`
	Families []Family    `json:"families"`
}

// Validate checks the vantage declaration.
func (v Vantage) Validate() error {
	if !canonicalRef(v.ID) {
		return errors.New("vantage id must be a canonical non-empty reference")
	}
	switch v.Kind {
	case VantageExternal, VantageLAN, VantageHost:
	default:
		return fmt.Errorf("vantage kind %q is unsupported", v.Kind)
	}
	if len(v.Families) == 0 {
		return errors.New("vantage declares no address family")
	}
	for _, family := range v.Families {
		if family != FamilyIPv4 && family != FamilyIPv6 {
			return fmt.Errorf("vantage family %q is unsupported", family)
		}
	}
	return nil
}

func (v Vantage) speaks(family Family) bool {
	for _, candidate := range v.Families {
		if candidate == family {
			return true
		}
	}
	return false
}

// Result is the transport-level outcome of one probe. It carries no payload:
// a probe opens a TCP connection and closes it without sending a byte.
type Result string

const (
	// ResultReachable means the TCP handshake completed.
	ResultReachable Result = "reachable"
	// ResultClosed means the address answered with a refusal.
	ResultClosed Result = "closed"
	// ResultFiltered means nothing answered before the timeout, or the path
	// reported the host unreachable.
	ResultFiltered Result = "filtered"
	// ResultVantageUnavailable means the vantage itself could not attempt the
	// probe (no route or no address of that family).
	ResultVantageUnavailable Result = "vantage_unavailable"
	// ResultError is any other failure, including a refused out-of-scope probe.
	ResultError Result = "error"
)

// Observation is one probe outcome of one owned address and port.
type Observation struct {
	TargetRef  string     `json:"targetRef"`
	Address    netip.Addr `json:"address"`
	Port       int        `json:"port"`
	Vantage    Vantage    `json:"vantage"`
	ObservedAt time.Time  `json:"observedAt"`
	Result     Result     `json:"result"`
	// Detail is a short stable class of the outcome, never a response body.
	Detail string `json:"detail,omitempty"`
}

func (o Observation) key() string { return probeKey(o.Address, o.Port) }

func probeKey(address netip.Addr, port int) string { return fmt.Sprintf("%s|%d", address, port) }

// Prober attempts one TCP connection from one vantage.
type Prober interface {
	Vantage() Vantage
	Probe(ctx context.Context, address netip.Addr, port int) (Result, string)
}

// Observe probes every address and port of the scope, and only those, in
// scope order. It is sequential and bounded by the scope bounds and by ctx;
// a cancelled context stops the run and returns what was observed so far.
func Observe(ctx context.Context, scope Scope, prober Prober, now func() time.Time) ([]Observation, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	vantage := prober.Vantage()
	if err := vantage.Validate(); err != nil {
		return nil, fmt.Errorf("exposureproof: %w", err)
	}
	var observations []Observation
	for _, target := range scope.Targets {
		for _, policy := range target.Ports {
			if err := ctx.Err(); err != nil {
				return observations, err
			}
			result, detail := prober.Probe(ctx, target.Address, policy.Port)
			observations = append(observations, Observation{
				TargetRef: target.Ref, Address: target.Address, Port: policy.Port, Vantage: vantage,
				ObservedAt: now().UTC(), Result: result, Detail: detail,
			})
		}
	}
	return observations, nil
}

// DefaultProbeTimeout bounds one connection attempt.
const DefaultProbeTimeout = 5 * time.Second

// TCPProber probes with plain TCP connects, forcing the address family of the
// target so an IPv6 probe never falls back to IPv4. It refuses every address
// and port outside the scope it was built from.
type TCPProber struct {
	vantage Vantage
	timeout time.Duration
	allowed map[string]bool
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewTCPProber builds a prober restricted to the scope.
func NewTCPProber(scope Scope, vantage Vantage, timeout time.Duration) (*TCPProber, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if err := vantage.Validate(); err != nil {
		return nil, fmt.Errorf("exposureproof: %w", err)
	}
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	allowed := map[string]bool{}
	for _, target := range scope.Targets {
		for _, policy := range target.Ports {
			allowed[probeKey(target.Address, policy.Port)] = true
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	return &TCPProber{vantage: vantage, timeout: timeout, allowed: allowed, dial: dialer.DialContext}, nil
}

// Vantage returns the declared vantage of the prober.
func (p *TCPProber) Vantage() Vantage { return p.vantage }

// Probe opens and immediately closes one TCP connection.
func (p *TCPProber) Probe(ctx context.Context, address netip.Addr, port int) (Result, string) {
	if !p.allowed[probeKey(address, port)] {
		return ResultError, "out_of_scope"
	}
	family := FamilyOf(address)
	if !p.vantage.speaks(family) {
		return ResultVantageUnavailable, "family_not_declared"
	}
	network := "tcp4"
	if family == FamilyIPv6 {
		network = "tcp6"
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := p.dial(ctx, network, netip.AddrPortFrom(address, uint16(port)).String())
	if err == nil {
		_ = conn.Close()
		return ResultReachable, "handshake_completed"
	}
	return classifyDialError(ctx, err)
}

func classifyDialError(ctx context.Context, err error) (Result, string) {
	var netErr net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return ResultClosed, "connection_refused"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EADDRNOTAVAIL), errors.Is(err, syscall.EAFNOSUPPORT):
		return ResultVantageUnavailable, "no_route_from_vantage"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return ResultFiltered, "host_unreachable"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return ResultFiltered, "timeout"
	case ctx.Err() != nil:
		return ResultError, "cancelled"
	default:
		return ResultError, "dial_failed"
	}
}

func digestLines(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
