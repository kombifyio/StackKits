// Package accessgate is the StackKits Access Gate (homelab identity target §7):
// the forward-auth decision every server runs in front of TinyAuth and, on the
// remote listener, in front of every route including endpoints with native
// authentication.
//
// The listener class is fixed by the router, never by the request: the LAN
// router calls the gate at /v1/gate/lan from its forward-auth middleware, the
// remote router binds /v1/gate/remote to its only entrypoint. A request
// through the remote listener is never treated as home, forwarded headers do
// not change that, and TinyAuth's parent-domain cookie is never consulted
// there. The route table contract lives in internal/accessgatetable.
package accessgate

import "github.com/kombifyio/stackkits/internal/accessgatetable"

// The route table contract, re-exported for the gate's callers.
type (
	Table     = accessgatetable.Table
	Route     = accessgatetable.Route
	RateLimit = accessgatetable.RateLimit
)

const (
	RoutesSchema = accessgatetable.RoutesSchema

	ListenerLAN    = accessgatetable.ListenerLAN
	ListenerRemote = accessgatetable.ListenerRemote

	ExposureLocalOnly     = accessgatetable.ExposureLocalOnly
	ExposureRemotePrivate = accessgatetable.ExposureRemotePrivate
	ExposurePublic        = accessgatetable.ExposurePublic

	AuthenticationNone        = accessgatetable.AuthenticationNone
	AuthenticationHuman       = accessgatetable.AuthenticationHuman
	AuthenticationHumanDevice = accessgatetable.AuthenticationHumanDevice
	AuthenticationAgent       = accessgatetable.AuthenticationAgent
	AuthenticationWorkload    = accessgatetable.AuthenticationWorkload

	PrivilegeUser     = accessgatetable.PrivilegeUser
	PrivilegeSignIn   = accessgatetable.PrivilegeSignIn
	PrivilegeAdmin    = accessgatetable.PrivilegeAdmin
	PrivilegeIdentity = accessgatetable.PrivilegeIdentity
	PrivilegeSecrets  = accessgatetable.PrivilegeSecrets
	PrivilegeVault    = accessgatetable.PrivilegeVault
	PrivilegeRecovery = accessgatetable.PrivilegeRecovery

	IngressForwardAuth = accessgatetable.IngressForwardAuth
	IngressNative      = accessgatetable.IngressNative
	IngressNone        = accessgatetable.IngressNone

	IdentityAdministrationPocketID = accessgatetable.IdentityAdministrationPocketID

	HeaderListener      = accessgatetable.HeaderListener
	HeaderSubject       = accessgatetable.HeaderSubject
	HeaderDevice        = accessgatetable.HeaderDevice
	HeaderAuthorization = accessgatetable.HeaderAuthorization
	HeaderDPoP          = accessgatetable.HeaderDPoP
	HeaderAgentEnvelope = accessgatetable.HeaderAgentEnvelope
	// HeaderGatewayAgentEnvelope carries the agent's Gateway-signed envelope.
	HeaderGatewayAgentEnvelope = accessgatetable.HeaderGatewayAgentEnvelope
	// HeaderAppPass carries an app pass; the router strips it before the backend.
	HeaderAppPass = accessgatetable.HeaderAppPass
)

var (
	// LANBoundPrivileges stay on the LAN listener unless the Owner opts in.
	LANBoundPrivileges = accessgatetable.LANBoundPrivileges
	// ResponseHeaders is the exact authResponseHeaders list of both gate
	// middlewares.
	ResponseHeaders = accessgatetable.ResponseHeaders
	// ErrInvalidTable rejects a table the gate cannot enforce exactly.
	ErrInvalidTable = accessgatetable.ErrInvalidTable
)

// ParseTable decodes and validates a route table strictly.
func ParseTable(raw []byte) (Table, error) { return accessgatetable.ParseTable(raw) }

func hasDotSegment(path string) bool { return accessgatetable.HasDotSegment(path) }

var httpMethods = accessgatetable.HTTPMethods
