package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
)

const (
	bridgePublicationTableSchema = "stackkit.bridge-publication-routes/v1"
	bridgePublicationTablePath   = ".stackkit/custody/bridge-publication/routes.json"
	bridgePublicationTableLimit  = 512 << 10
)

// BridgePublicationOriginProbe reads one origin target back through the
// activated federation link and returns the observed HTTP status (0 for a TCP
// probe). Tests replace it; production uses the link custody and mTLS client
// credential the federation link owner already holds.
type BridgePublicationOriginProbe func(ctx context.Context, root string, probe architecturev2renderer.BridgePublicationHealthProbe) (int, error)

// bridgePublicationRouteTable is the Owner-signed default-closed Cloud edge
// route table. It holds the governed publication rules and nothing else: no
// endpoint a caller chose, no credential, no key. Origin credentials and the
// auth verifier are resolved from node custody when the edge serves.
type bridgePublicationRouteTable struct {
	Schema       string                                         `json:"schema"`
	Binding      localevidence.LocalBinding                     `json:"binding"`
	PolicyDigest string                                         `json:"policyDigest"`
	StackID      string                                         `json:"stackId"`
	Publications []architecturev2renderer.BridgePublicationRule `json:"publications"`
	Signature    localevidence.OwnerPolicyStateSignature        `json:"signature"`
}

type osBridgePublicationOperations struct {
	root   string
	probe  BridgePublicationOriginProbe
	served BridgePublicationServedProbe
	now    func() time.Time
	mu     sync.Mutex
}

// NewOSBridgePublicationOperations selects the local node as the owner of the
// Cloud edge route table. Product composition must opt in, and only inside a
// capability-admitted Advanced mutation.
func NewOSBridgePublicationOperations(workspaceRoot string) (BridgePublicationOperations, error) {
	return newOSBridgePublicationOperations(workspaceRoot, probeBridgePublicationOrigin, time.Now)
}

func newOSBridgePublicationOperations(workspaceRoot string, probe BridgePublicationOriginProbe, now func() time.Time) (*osBridgePublicationOperations, error) {
	root, err := ownerWorkspaceRoot(workspaceRoot, "local bridge publication")
	if err != nil {
		return nil, err
	}
	return &osBridgePublicationOperations{root: root, probe: probe, served: probeBridgePublicationServed, now: now}, nil
}

func (o *osBridgePublicationOperations) checkTarget(site, node, channel string) (localevidence.LocalBinding, error) {
	owner, err := localevidence.LoadOwnerCustody(o.root)
	if err != nil {
		return localevidence.LocalBinding{}, err
	}
	if owner.Binding.SiteRef != site || owner.Binding.NodeRef != node || owner.Binding.ChannelRef != channel {
		return localevidence.LocalBinding{}, errors.New("bridge publication: target differs from current local custody")
	}
	return owner.Binding, nil
}

func (o *osBridgePublicationOperations) ApplyServicePublications(ctx context.Context, policy BridgePublicationApplyPolicy) (BridgePublicationObservation, error) {
	if err := ctx.Err(); err != nil {
		return BridgePublicationObservation{}, err
	}
	binding, err := o.checkTarget(policy.SiteRef, policy.NodeRef, policy.ExecutionChannelRef)
	if err != nil {
		return BridgePublicationObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// Withdraw first: a failed replacement must not keep a previously
	// authorized route serving. The edge is closed until the write lands.
	if err := o.withdraw(); err != nil {
		return BridgePublicationObservation{}, err
	}
	if err := o.writeTable(bridgePublicationRouteTable{
		Schema: bridgePublicationTableSchema, Binding: binding, PolicyDigest: policy.PolicyDigest,
		StackID: policy.StackID, Publications: cloneBridgePublicationRules(policy.Publications),
	}); err != nil {
		return BridgePublicationObservation{}, err
	}
	return o.observe(ctx, BridgePublicationExpectation(policy), "applied", false)
}

func (o *osBridgePublicationOperations) RemoveObsoleteServicePublications(ctx context.Context, expectation BridgePublicationExpectation) (BridgePublicationObservation, error) {
	if err := ctx.Err(); err != nil {
		return BridgePublicationObservation{}, err
	}
	if _, err := o.checkTarget(expectation.SiteRef, expectation.NodeRef, expectation.ExecutionChannelRef); err != nil {
		return BridgePublicationObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	table, err := o.readTable()
	if err != nil {
		return BridgePublicationObservation{}, err
	}
	if table.PolicyDigest != expectation.PolicyDigest {
		return BridgePublicationObservation{}, errors.New("bridge publication: live route table belongs to another policy")
	}
	// Apply wrote exactly the policy, so this only withdraws what is foreign.
	kept := table.Publications[:0:0]
	for _, rule := range table.Publications {
		for _, want := range expectation.Publications {
			if reflect.DeepEqual(rule, want) {
				kept = append(kept, rule)
				break
			}
		}
	}
	if len(kept) != len(table.Publications) {
		table.Publications = kept
		if err := o.writeTable(table); err != nil {
			return BridgePublicationObservation{}, err
		}
	}
	return o.observe(ctx, expectation, "obsolete-removed", false)
}

func (o *osBridgePublicationOperations) VerifyServicePublications(ctx context.Context, expectation BridgePublicationExpectation) (BridgePublicationObservation, error) {
	if err := ctx.Err(); err != nil {
		return BridgePublicationObservation{}, err
	}
	if _, err := o.checkTarget(expectation.SiteRef, expectation.NodeRef, expectation.ExecutionChannelRef); err != nil {
		return BridgePublicationObservation{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.observe(ctx, expectation, "ready", true)
}

// observe holds the live table to exactly the expected rules. With backends
// it also reads every origin target back through the link; without them the
// observation never claims readiness.
func (o *osBridgePublicationOperations) observe(ctx context.Context, expectation BridgePublicationExpectation, status string, backends bool) (BridgePublicationObservation, error) {
	table, err := o.readTable()
	if err != nil {
		return BridgePublicationObservation{}, err
	}
	if table.PolicyDigest != expectation.PolicyDigest || table.StackID != expectation.StackID ||
		len(table.Publications) != len(expectation.Publications) {
		return BridgePublicationObservation{}, errors.New("bridge publication: live route table differs from the expected policy")
	}
	for index, rule := range expectation.Publications {
		if !reflect.DeepEqual(table.Publications[index], rule) {
			return BridgePublicationObservation{}, errors.New("bridge publication: live route table differs from the expected policy")
		}
	}
	publications := make([]BridgePublicationRuleObservation, len(expectation.Publications))
	readbacks := make([][]BridgePublicationBackendObservation, len(expectation.Publications))
	if backends {
		for index, rule := range expectation.Publications {
			if rule.HealthProbe == nil {
				return BridgePublicationObservation{}, errors.New("bridge publication: rule has no executable Health probe")
			}
			for _, target := range rule.OriginTargets {
				code, probeErr := o.probe(ctx, o.root, *rule.HealthProbe)
				if probeErr != nil {
					return BridgePublicationObservation{}, fmt.Errorf("bridge publication: origin %s/%s unreachable through the link: %w", target.NodeRef, target.InstanceRef, probeErr)
				}
				if rule.HealthProbe.Kind == "http" && !containsStatus(rule.HealthProbe.ExpectedStatuses, code) {
					return BridgePublicationObservation{}, fmt.Errorf("bridge publication: origin %s/%s answered %d, outside the governed Health statuses", target.NodeRef, target.InstanceRef, code)
				}
				readbacks[index] = append(readbacks[index], BridgePublicationBackendObservation{
					NodeRef: target.NodeRef, InstanceRef: target.InstanceRef, HealthGateRef: rule.HealthGateRef,
					Status: "healthy", HTTPStatus: code, ObservedAt: o.now().UTC().Format(time.RFC3339Nano),
				})
			}
		}
	}
	// A route is reported served only after a real request through the edge
	// listener reached the pinned origin; without a declared listener the
	// observation makes no served claim.
	served := make([]*BridgePublicationServedObservation, len(expectation.Publications))
	if backends {
		for index, rule := range expectation.Publications {
			proof, err := o.served(ctx, o.root, rule, o.now())
			if err != nil {
				return BridgePublicationObservation{}, err
			}
			served[index] = proof
		}
	}
	// Configuration-time fields are read back from the table just verified;
	// request-time enforcement is the edge listener's, proven by ServedReadback.
	stamp := o.now().UTC().Format(time.RFC3339Nano)
	for index, rule := range expectation.Publications {
		publications[index] = BridgePublicationRuleObservation{
			ServiceRef: rule.ServiceRef, SourceSiteRef: rule.SourceSiteRef, EdgeSiteRef: rule.EdgeSiteRef,
			Host: rule.Host, Protocol: rule.Protocol, Port: rule.Port, Path: rule.Path,
			TLSMinVersion: rule.TLSMinVersion, AuthPolicyRef: rule.AuthPolicyRef,
			OriginIdentityRef: rule.OriginIdentityRef, RateLimitRequests: rule.RateLimitRequests,
			RateLimitWindowSeconds: rule.RateLimitWindowSeconds, ModuleRef: rule.ModuleRef, UnitRef: rule.UnitRef,
			OriginNodeRefs:     append([]string(nil), rule.OriginNodeRefs...),
			OriginInstanceRefs: append([]string(nil), rule.OriginInstanceRefs...),
			OriginTargets:      append([]architecturev2renderer.BridgePublicationOriginTarget(nil), rule.OriginTargets...),
			UpstreamProtocol:   rule.UpstreamProtocol, TargetPort: rule.TargetPort,
			HealthGateRef: rule.HealthGateRef, DataBindingRef: rule.DataBindingRef,
			Authentication: rule.Authentication, Privilege: rule.Privilege,
			EnrolledDeviceRequired: rule.EnrolledDeviceRequired, OwnerStepUpRequired: rule.OwnerStepUpRequired,
			AllowedMethods:        append([]string(nil), rule.AllowedMethods...),
			PublicationConfigured: true, DefaultClosed: true, OriginMTLSRequired: true,
			OriginIdentityBound: rule.OriginIdentityRef != "", TLSPolicyBound: rule.TLSMinVersion != "",
			AuthenticationBound: rule.AuthPolicyRef != "", RateLimitBound: rule.RateLimitRequests > 0 && rule.RateLimitWindowSeconds > 0,
			ConfigurationObservedAt: stamp, VerifierPolicyObservedAt: stamp, TLSPolicyObservedAt: stamp,
			BackendReadback: readbacks[index], ServedReadback: served[index],
		}
	}
	return BridgePublicationObservation{
		PolicyDigest: expectation.PolicyDigest, Status: status, EvaluatedAt: expectation.EvaluatedAt,
		ObservedAt: o.now().UTC().Format(time.RFC3339Nano), Publications: publications,
	}, nil
}

func containsStatus(statuses []int, code int) bool {
	for _, status := range statuses {
		if status == code {
			return true
		}
	}
	return false
}

func (o *osBridgePublicationOperations) withdraw() error {
	err := os.Remove(filepath.Join(o.root, filepath.FromSlash(bridgePublicationTablePath)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("bridge publication: withdraw route table: %w", err)
	}
	return nil
}

func (o *osBridgePublicationOperations) writeTable(table bridgePublicationRouteTable) error {
	table.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(table)
	if err != nil {
		return err
	}
	table.Signature, err = localevidence.SignOwnerPolicyState(o.root, unsigned)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(table)
	if err != nil {
		return err
	}
	fs, err := confinedfs.Open(o.root)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	directory := filepath.ToSlash(filepath.Dir(bridgePublicationTablePath))
	if err := tx.MkdirAll(directory, 0700); err != nil {
		return err
	}
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(bridgePublicationTablePath, raw)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("bridge publication: route table was not durably installed")
	}
	_, err = tx.SyncDirectory(directory)
	return err
}

// readTable returns the verified table. An absent, unsigned, substituted or
// foreign-target table is an error: the edge stays closed.
func (o *osBridgePublicationOperations) readTable() (bridgePublicationRouteTable, error) {
	return readBridgePublicationTable(o.root)
}

// readBridgePublicationTable is shared by the owner and the edge listener, so
// both hold the table to the same signature and custody check.
func readBridgePublicationTable(root string) (bridgePublicationRouteTable, error) {
	var table bridgePublicationRouteTable
	fs, err := confinedfs.Open(root)
	if err != nil {
		return table, err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return table, err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStableBounded(bridgePublicationTablePath, bridgePublicationTableLimit)
	if err != nil {
		return table, fmt.Errorf("bridge publication: route table is not installed: %w", err)
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		return table, err
	}
	signature := table.Signature
	table.Signature = localevidence.OwnerPolicyStateSignature{}
	unsigned, err := json.Marshal(table)
	if err != nil {
		return table, err
	}
	if err := localevidence.VerifyOwnerPolicyState(root, unsigned, signature); err != nil {
		return table, err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return table, err
	}
	if table.Schema != bridgePublicationTableSchema || table.Binding != owner.Binding {
		return table, errors.New("bridge publication: substituted route table")
	}
	return table, nil
}

// activatedCloudFederationLink returns the one activated Cloud federation link:
// the only upstream the Cloud edge may use.
func activatedCloudFederationLink(root string) (*WireGuardFabricCustody, error) {
	records, err := activatedWireGuardCustodies(root)
	if err != nil {
		return nil, err
	}
	var link *WireGuardFabricCustody
	for index := range records {
		if records[index].Fabric.SiteKind == "cloud" {
			if link != nil {
				return nil, errors.New("bridge publication: more than one activated Cloud federation link")
			}
			link = &records[index].Fabric
		}
	}
	if link == nil {
		return nil, errors.New("bridge publication: no activated Cloud federation link")
	}
	return link, nil
}

// probeBridgePublicationOrigin uses the one activated Cloud federation link:
// its loopback origin socket, pinned origin name and mTLS peer credential.
func probeBridgePublicationOrigin(ctx context.Context, root string, probe architecturev2renderer.BridgePublicationHealthProbe) (int, error) {
	link, err := activatedCloudFederationLink(root)
	if err != nil {
		return 0, err
	}
	switch probe.Kind {
	case "tcp":
		timeout := time.Duration(probe.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", link.OriginSocket)
		if err != nil {
			return 0, err
		}
		return 0, connection.Close()
	case "http":
		method := probe.Method
		if method == "" {
			method = http.MethodGet
		}
		return localorigin.ProbePeerOriginRequest(ctx, root, link.PeerRef, link.OriginSocket, link.OriginServerName, method, probe.Path)
	}
	return 0, errors.New("bridge publication: unsupported Health probe kind")
}

var _ BridgePublicationOperations = (*osBridgePublicationOperations)(nil)
