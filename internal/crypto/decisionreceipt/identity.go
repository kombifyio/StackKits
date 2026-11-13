package decisionreceipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// NodeIdentity is the node's own enrolled identity: the tenant and the server
// the platform enrolled this node as. It is read only from root-owned node
// custody, never from an argument, so a receipt bound to one node is refused
// on its twin.
type NodeIdentity struct {
	TenantID string
	ServerID string
	// OwnerID is the platform subject of the installation's Owner, pinned at
	// the hosted enrollment. It is the only id a person principal may carry
	// (docs/DECISION-RECEIPTS.md, "Acting principal"); empty when the
	// enrollment named no Owner, and then no person principal is admitted.
	OwnerID string
}

// nodeIdentityDocument is the record the privileged installer pins after it
// admitted the hosted enrollment (techstack/pkg/ril/nodeproof
// PinLocalIdentity). An empty record is the installer's tombstone after a
// re-enrollment into a non-hosted authority.
type nodeIdentityDocument struct {
	TenantID string `json:"tenantId"`
	OwnerID  string `json:"ownerId,omitempty"`
	ServerID string `json:"serverId"`
	StackID  string `json:"stackId,omitempty"`
}

// enrolledIDPattern is the stable identifier shape the node proof verifier
// admits for the same record.
var enrolledIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,191}$`)

// enrolledOwnerPattern is the platform subject shape of the enrolled Owner
// (for example auth0|owner-0001): bounded, printable and without whitespace.
var enrolledOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:|@+-]{0,255}$`)

// loadNodeIdentity reads the node's enrolled identity from custody.
func (c custody) loadNodeIdentity() (NodeIdentity, error) {
	raw, err := c.readCustodyFile(c.identityPath)
	if err != nil {
		return NodeIdentity{}, err
	}
	var document nodeIdentityDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return NodeIdentity{}, errors.New("the enrolled node identity is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return NodeIdentity{}, errors.New("the enrolled node identity carries trailing data")
	}
	if !enrolledIDPattern.MatchString(document.TenantID) || !enrolledIDPattern.MatchString(document.ServerID) {
		return NodeIdentity{}, errors.New("this node holds no enrolled identity")
	}
	owner := ""
	if document.OwnerID != "" {
		if !enrolledOwnerPattern.MatchString(document.OwnerID) {
			return NodeIdentity{}, errors.New("the enrolled Owner id is invalid")
		}
		owner = document.OwnerID
	}
	return NodeIdentity{TenantID: document.TenantID, ServerID: document.ServerID, OwnerID: owner}, nil
}

// EnrolledOwnerPrincipal is the person principal of this node's enrolled
// Owner, read from root-owned node custody: the one person id a filer files
// for the Owner (docs/DECISION-RECEIPTS.md, "Acting principal").
func EnrolledOwnerPrincipal() (Principal, error) {
	node, err := nodeCustody().loadNodeIdentity()
	if err != nil {
		return Principal{}, err
	}
	if node.OwnerID == "" {
		return Principal{}, errors.New("the node's enrolled identity names no Owner")
	}
	return Principal{Kind: PrincipalPerson, ID: node.OwnerID}, nil
}

// EnvelopeAgentPrincipal is the agent instance principal of one Gateway
// agent envelope, spelled as the platform files it:
// gateway:<link ref>:<agent instance>/<account>.
func EnvelopeAgentPrincipal(linkRef, instance, account string) (Principal, error) {
	for _, part := range []string{linkRef, instance, account} {
		if part == "" || part != strings.TrimSpace(part) || strings.ContainsAny(part, "/") || strings.ContainsFunc(part, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return Principal{}, errors.New("the agent envelope does not name a link, an agent instance and an account")
		}
	}
	return Principal{Kind: PrincipalAgentInstance, ID: "gateway:" + linkRef + ":" + instance + "/" + account}, nil
}
