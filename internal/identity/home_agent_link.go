package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kombifyio/stackkits/internal/guardenrollment"
)

// The agent link of a node (homelab identity target §7, §10; Gateway ADR 0042
// "Agent node envelope contract"). When Techstack registers a Guard-managed
// server as a Connect techstack-node, the Gateway answers with the node's agent
// envelope verification key; Techstack stores it per server and hands that key
// document, the server and tenant it was issued for, the node owner's kombify
// subject and the D10 references of the linked household members (kombify
// subject and Pocket ID username) to the node over the Guard (the capability
// gated StackKit operation AGENT_LINK_INSTALL). The node pins exactly that key
// and never learns or refreshes one from a request, an envelope or a URL.
// Pinning a key, re-keying and changing the owner's subject are Owner acts on
// homelab identity: they need the Owner's passkey step-up at the Authority
// (class agent-link, bound to exactly that request), approved in the Hub, never
// Techstack's assertion alone. Under the pinned key Techstack may update the
// D10-linked members (it is the D10 link authority) without a step-up. A
// higher binding generation or a new Gateway master key id replaces the pin;
// the same key id with another key, a lower generation, a key that was pinned
// before, or another node, server or tenant is refused. The owner's subject
// maps only to the Owner whose custody this node holds, and no member can map
// to the Owner's account. The record holds no credential: the key is public,
// the references are opaque subjects.
const (
	HomeAgentLinkSchema        = "stackkit.home-agent-link/v1"
	HomeAgentLinkRequestSchema = "stackkit.home-agent-link-request/v1"
	// HomeAgentEnvelopeType, HomeAgentEnvelopeIssuer and
	// HomeAgentEnvelopeHeader are the Gateway's envelope contract.
	HomeAgentEnvelopeType   = "kombify-agent-envelope+jwt"
	HomeAgentEnvelopeIssuer = "kombify-gateway"
	HomeAgentEnvelopeHeader = "X-Kombify-Agent-Envelope"

	homeAgentLinkPath        = ".stackkit/custody/home-verifier/agent-link.json"
	homeAgentLinkMaxBytes    = 64 << 10
	homeAgentLinkMaxSubjects = 64
	homeAgentLinkMaxRetired  = 16
)

// The capabilities an envelope can carry (the Gateway's
// AGENT_NODE_ENVELOPE_CAPABILITIES). Read is the ceiling of a read-only lease.
const (
	HomeAgentCapabilityRead        = "techstack.inventory.read"
	HomeAgentCapabilityOperate     = "techstack.inventory.operate"
	HomeAgentCapabilityMaintenance = "techstack.server.maintenance"
)

var (
	// ErrHomeAgentLinkAbsent means no Gateway key is pinned on this node.
	ErrHomeAgentLinkAbsent = errors.New("identity: no Gateway agent key is pinned at linking on this node")
	// ErrHomeAgentLinkRefused refuses a link the node cannot pin exactly.
	ErrHomeAgentLinkRefused = errors.New("identity: the agent link is not a valid Gateway key document for this node")

	homeAgentNodeID       = regexp.MustCompile(`^tsn_[0-9A-HJKMNP-TV-Z]{26}$`)
	homeAgentKeyID        = regexp.MustCompile(`^[A-Za-z0-9._-]{1,48}\.([1-9][0-9]{0,17})$`)
	homeAgentRef          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	homeAgentPocketIDUser = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	homeAgentUsername     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// HomeAgentJWK is the public Ed25519 key of the envelope key document.
type HomeAgentJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

// HomeAgentKey is the Gateway's agent_envelope member, verbatim.
type HomeAgentKey struct {
	Alg               string       `json:"alg"`
	Typ               string       `json:"typ"`
	Iss               string       `json:"iss"`
	Aud               string       `json:"aud"`
	Kid               string       `json:"kid"`
	JWK               HomeAgentJWK `json:"jwk"`
	JKT               string       `json:"jkt"`
	BindingGeneration int64        `json:"binding_generation"`
}

// HomeAgentSubject is one D10 reference: the kombify account (Auth0) subject
// and the Homelab account (Pocket ID subject) it is linked to.
type HomeAgentSubject struct {
	CloudSubject string `json:"cloudSubject"`
	Account      string `json:"account"`
}

// HomeAgentLinkMember is one linked household member as Techstack's D10
// reference names it: the kombify account subject and the Pocket ID username.
type HomeAgentLinkMember struct {
	CloudSubject string `json:"cloudSubject"`
	Username     string `json:"username"`
}

// HomeAgentLinkRequest is what Techstack hands the node at linking.
// OwnerSubject is the kombify subject of the node's owner (the Connect node
// owner, the only person the Gateway issues envelopes for today); Members are
// the household members whose D10 link is verified.
type HomeAgentLinkRequest struct {
	Schema        string                `json:"schema"`
	ServerID      string                `json:"serverId"`
	TenantID      string                `json:"tenantId"`
	AgentEnvelope HomeAgentKey          `json:"agentEnvelope"`
	OwnerSubject  string                `json:"ownerSubject"`
	Members       []HomeAgentLinkMember `json:"members"`
}

// HomeAgentAccountResolver resolves a Pocket ID username to the Homelab
// account (Pocket ID subject) of an enabled user at the Authority.
type HomeAgentAccountResolver func(username string) (string, error)

// HomeAgentAccounts resolves the Homelab accounts of a link at the Authority:
// OwnerUsername is the Pocket ID username of the Owner whose custody this node
// holds (never one a request names) and Resolve maps a username to the
// enabled account.
type HomeAgentAccounts struct {
	OwnerUsername string
	Resolve       HomeAgentAccountResolver
}

// HomeAgentLink is the pinned link of this node. OwnerSubject is the kombify
// subject mapped to the Owner of this node's custody; RetiredKeys are the
// thumbprints of keys pinned before, which never return.
type HomeAgentLink struct {
	Schema       string             `json:"schema"`
	ServerID     string             `json:"serverId"`
	TenantID     string             `json:"tenantId"`
	Key          HomeAgentKey       `json:"key"`
	OwnerSubject string             `json:"ownerSubject"`
	Subjects     []HomeAgentSubject `json:"subjects"`
	RetiredKeys  []string           `json:"retiredKeys,omitempty"`
	InstalledAt  time.Time          `json:"installedAt"`
}

// DecodeHomeAgentLinkRequest decodes a link request strictly.
func DecodeHomeAgentLinkRequest(raw []byte) (HomeAgentLinkRequest, error) {
	var request HomeAgentLinkRequest
	if len(raw) == 0 || len(raw) > homeAgentLinkMaxBytes {
		return request, ErrHomeAgentLinkRefused
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Schema != HomeAgentLinkRequestSchema {
		return HomeAgentLinkRequest{}, ErrHomeAgentLinkRefused
	}
	return request, nil
}

// InstallHomeAgentLink pins the Gateway key and the D10 references of this
// node. The link is for this node's own Guard enrollment only. The caller has
// the Owner approve the request with a passkey step-up first whenever
// HomeAgentLinkNeedsOwner says so. A higher binding generation or a new Gateway
// master key id (the kid prefix) replaces the pin; under the same kid only the
// references may change; a lower generation, a retired key or another node,
// server or tenant is refused (remove the link first).
func InstallHomeAgentLink(root string, request HomeAgentLinkRequest, accounts HomeAgentAccounts, now time.Time) (HomeAgentLink, error) {
	link, err := prepareHomeAgentLink(root, request, accounts, now)
	if err != nil {
		return HomeAgentLink{}, err
	}
	raw, err := json.Marshal(link)
	if err != nil {
		return HomeAgentLink{}, err
	}
	if err := writeHomePrivate(root, homeAgentLinkPath, raw, false); err != nil {
		return HomeAgentLink{}, err
	}
	return link, nil
}

// CheckHomeAgentLink runs every check of InstallHomeAgentLink without pinning,
// so a request that could never be pinned raises no Owner step-up.
func CheckHomeAgentLink(root string, request HomeAgentLinkRequest, accounts HomeAgentAccounts) error {
	_, err := prepareHomeAgentLink(root, request, accounts, time.Now())
	return err
}

func prepareHomeAgentLink(root string, request HomeAgentLinkRequest, accounts HomeAgentAccounts, now time.Time) (HomeAgentLink, error) {
	if request.Schema != HomeAgentLinkRequestSchema || accounts.Resolve == nil || len(request.Members) >= homeAgentLinkMaxSubjects {
		return HomeAgentLink{}, ErrHomeAgentLinkRefused
	}
	// The link is for this node only: its server is the server of this
	// node's own Guard enrollment, never one a request names.
	enrollment, err := guardenrollment.Read()
	if err != nil || enrollment.ServerID != request.ServerID {
		return HomeAgentLink{}, ErrHomeAgentLinkRefused
	}
	subjects, err := resolveHomeAgentSubjects(request, accounts)
	if err != nil {
		return HomeAgentLink{}, err
	}
	if err := validateHomeAgentLink(request.ServerID, request.TenantID, request.AgentEnvelope, subjects); err != nil {
		return HomeAgentLink{}, err
	}
	var retired []string
	prior, err := LoadHomeAgentLink(root)
	switch {
	case err == nil:
		if !homeAgentLinkReplaces(prior, request) {
			return HomeAgentLink{}, ErrHomeAgentLinkRefused
		}
		retired = slices.Clone(prior.RetiredKeys)
		if prior.Key.JKT != request.AgentEnvelope.JKT {
			retired = append(retired, prior.Key.JKT)
			if len(retired) > homeAgentLinkMaxRetired {
				retired = retired[len(retired)-homeAgentLinkMaxRetired:]
			}
		}
	case !errors.Is(err, ErrHomeAgentLinkAbsent):
		return HomeAgentLink{}, err
	}
	link := HomeAgentLink{
		Schema: HomeAgentLinkSchema, ServerID: request.ServerID, TenantID: request.TenantID,
		Key: request.AgentEnvelope, OwnerSubject: request.OwnerSubject, Subjects: subjects, RetiredKeys: retired,
		InstalledAt: now.UTC().Truncate(time.Second),
	}
	return link, nil
}

// HomeAgentLinkNeedsOwner reports whether installing the request is an Owner
// act that needs the passkey step-up: always without a pinned link, and
// whenever the key, the node, server or tenant, or the owner's subject would
// change. Only a change of the D10-linked members under exactly the pinned key
// and owner is Techstack's to deliver alone.
func HomeAgentLinkNeedsOwner(root string, request HomeAgentLinkRequest) bool {
	prior, err := LoadHomeAgentLink(root)
	return err != nil || prior.Key != request.AgentEnvelope || prior.ServerID != request.ServerID ||
		prior.TenantID != request.TenantID || prior.OwnerSubject != request.OwnerSubject
}

// HomeAgentLinkStepUpAction is the Owner step-up that approves installing
// exactly this link request: the key document, server, tenant, owner subject
// and members. A re-key or a change of owner is another request and needs its
// own step-up.
func HomeAgentLinkStepUpAction(request HomeAgentLinkRequest) (HomeStepUpAction, error) {
	return NewHomeStepUpAction(HomeStepUpAgentLink, request)
}

// homeAgentLinkReplaces decides whether a request may replace the pinned link.
// Another node, server or tenant is another link (remove the current one
// first). A higher binding generation is a new binding; at the same generation
// another kid is a new Gateway master key (ADR 0042: recovery is a new master
// key id and a re-link), while the same kid must carry exactly the pinned key,
// so only the references can change. A lower generation never returns.
func homeAgentLinkReplaces(prior HomeAgentLink, request HomeAgentLinkRequest) bool {
	key := request.AgentEnvelope
	if prior.Key.Aud != key.Aud || prior.ServerID != request.ServerID || prior.TenantID != request.TenantID ||
		slices.Contains(prior.RetiredKeys, key.JKT) {
		return false
	}
	switch {
	case key.BindingGeneration > prior.Key.BindingGeneration:
		return true
	case key.BindingGeneration < prior.Key.BindingGeneration:
		return false
	case key.Kid != prior.Key.Kid:
		return key.JWK != prior.Key.JWK
	}
	return key == prior.Key
}

// resolveHomeAgentSubjects maps the owner's kombify subject to the Owner whose
// custody this node holds and each member's username to its enabled account.
// No member may name the Owner's username or resolve to the Owner's account,
// so a member reference never carries the Owner's groups.
func resolveHomeAgentSubjects(request HomeAgentLinkRequest, accounts HomeAgentAccounts) ([]HomeAgentSubject, error) {
	if !homeAgentUsername.MatchString(accounts.OwnerUsername) {
		return nil, ErrHomeAgentLinkRefused
	}
	owner, err := accounts.Resolve(accounts.OwnerUsername)
	if err != nil {
		return nil, fmt.Errorf("identity: the Owner has no enabled Homelab account yet: %w", err)
	}
	subjects := make([]HomeAgentSubject, 0, len(request.Members)+1)
	subjects = append(subjects, HomeAgentSubject{CloudSubject: request.OwnerSubject, Account: owner})
	for _, member := range request.Members {
		if !homeAgentUsername.MatchString(member.Username) || member.Username == accounts.OwnerUsername {
			return nil, ErrHomeAgentLinkRefused
		}
		account, err := accounts.Resolve(member.Username)
		if err != nil {
			return nil, fmt.Errorf("identity: the linked member %q has no enabled Homelab account: %w", member.Username, err)
		}
		if account == owner {
			return nil, ErrHomeAgentLinkRefused
		}
		subjects = append(subjects, HomeAgentSubject{CloudSubject: member.CloudSubject, Account: account})
	}
	return subjects, nil
}

// HomeAgentLinkPinned reports whether the request asks for exactly the link
// already pinned (same key, server, tenant and resolved subjects), so a
// repeated dispatch changes nothing.
func HomeAgentLinkPinned(root string, request HomeAgentLinkRequest, accounts HomeAgentAccounts) bool {
	prior, err := LoadHomeAgentLink(root)
	if err != nil || accounts.Resolve == nil || prior.Key != request.AgentEnvelope || prior.ServerID != request.ServerID || prior.TenantID != request.TenantID {
		return false
	}
	subjects, err := resolveHomeAgentSubjects(request, accounts)
	return err == nil && slices.Equal(subjects, prior.Subjects)
}

// HomeAgentLinkRequestDigest is the "sha256:<hex>" digest of the exact request
// bytes the node read; Techstack checks it against what it dispatched.
func HomeAgentLinkRequestDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// RemoveHomeAgentLink deletes the pin (the node was unlinked: Connect revoke,
// techstack-node.revoked or a Guard detach). Every envelope fails afterwards,
// also one the gate admitted but the endpoint has not consumed yet.
func RemoveHomeAgentLink(root string) (bool, error) {
	err := os.Remove(filepath.Join(root, filepath.FromSlash(homeAgentLinkPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// LoadHomeAgentLink reads and validates the pinned link.
func LoadHomeAgentLink(root string) (HomeAgentLink, error) {
	raw, err := readHomePrivate(root, homeAgentLinkPath, homeAgentLinkMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return HomeAgentLink{}, ErrHomeAgentLinkAbsent
	}
	if err != nil {
		return HomeAgentLink{}, err
	}
	var link HomeAgentLink
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&link) != nil || decoder.Decode(new(any)) != io.EOF || link.Schema != HomeAgentLinkSchema {
		return HomeAgentLink{}, ErrHomeAgentLinkRefused
	}
	if err := validateHomeAgentLink(link.ServerID, link.TenantID, link.Key, link.Subjects); err != nil {
		return HomeAgentLink{}, err
	}
	if len(link.Subjects) == 0 || link.Subjects[0].CloudSubject != link.OwnerSubject || len(link.RetiredKeys) > homeAgentLinkMaxRetired {
		return HomeAgentLink{}, ErrHomeAgentLinkRefused
	}
	return link, nil
}

// HomeAgentLinkAccounts lists the Homelab accounts the link maps agents to,
// so the household group projection knows their groups.
func HomeAgentLinkAccounts(root string) []string {
	link, err := LoadHomeAgentLink(root)
	if err != nil {
		return nil
	}
	accounts := make([]string, 0, len(link.Subjects))
	for _, subject := range link.Subjects {
		accounts = append(accounts, subject.Account)
	}
	return accounts
}

// publicKey returns the pinned Ed25519 key.
func (k HomeAgentKey) publicKey() (ed25519.PublicKey, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(k.JWK.X)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(raw), true
}

func validateHomeAgentLink(serverID, tenantID string, key HomeAgentKey, subjects []HomeAgentSubject) error {
	if !homeAgentRef.MatchString(serverID) || !homeAgentRef.MatchString(tenantID) {
		return ErrHomeAgentLinkRefused
	}
	if key.Alg != "EdDSA" || key.Typ != HomeAgentEnvelopeType || key.Iss != HomeAgentEnvelopeIssuer || !homeAgentNodeID.MatchString(key.Aud) ||
		key.JWK.Kty != "OKP" || key.JWK.Crv != "Ed25519" || key.BindingGeneration < 1 {
		return ErrHomeAgentLinkRefused
	}
	match := homeAgentKeyID.FindStringSubmatch(key.Kid)
	if match == nil || match[1] != strconv.FormatInt(key.BindingGeneration, 10) {
		return ErrHomeAgentLinkRefused
	}
	if _, ok := key.publicKey(); !ok || key.JKT != homeAgentJWKThumbprint(key.JWK.X) {
		return ErrHomeAgentLinkRefused
	}
	if len(subjects) == 0 || len(subjects) > homeAgentLinkMaxSubjects {
		return ErrHomeAgentLinkRefused
	}
	clouds, accounts := map[string]bool{}, map[string]bool{}
	for _, subject := range subjects {
		if !validHomeAgentCloudSubject(subject.CloudSubject) || !homeAgentPocketIDUser.MatchString(subject.Account) ||
			clouds[subject.CloudSubject] || accounts[subject.Account] {
			return ErrHomeAgentLinkRefused
		}
		clouds[subject.CloudSubject], accounts[subject.Account] = true, true
	}
	return nil
}

// homeAgentJWKThumbprint is the RFC 7638 thumbprint of an Ed25519 OKP key:
// base64url(SHA-256 of {"crv":"Ed25519","kty":"OKP","x":"<x>"}).
func homeAgentJWKThumbprint(x string) string {
	digest := sha256.Sum256([]byte(`{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// validHomeAgentCloudSubject admits an Auth0 subject: printable, no space,
// at most 255 bytes.
func validHomeAgentCloudSubject(value string) bool {
	if value == "" || len(value) > 255 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || r == ' ' {
			return false
		}
	}
	return true
}
