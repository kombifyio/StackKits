package resolvedplan

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	externalHostBindingAPIVersion    = "stackkit.external-host-binding/v1"
	hostConformanceReceiptAPIVersion = "stackkit.host-conformance-receipt/v1"
)

var forbiddenExternalHostKeys = map[string]struct{}{
	"provider": {}, "providerid": {}, "providerref": {},
	"account": {}, "accountid": {}, "accountref": {},
	"datacenter": {}, "datacenterid": {}, "region": {}, "zone": {}, "location": {},
	"image": {}, "imageid": {}, "size": {}, "sizeref": {},
	"nativeresource": {}, "nativeresourceid": {}, "resourceid": {}, "resourcegroup": {},
	"serverid": {}, "instanceid": {}, "machineid": {},
	"credential": {}, "credentials": {}, "credentialref": {},
	"ownership": {}, "ownershipledger": {}, "cleanup": {}, "cleanupref": {},
	"address": {}, "publicaddress": {}, "privateaddress": {}, "managementaddress": {}, "ipaddress": {}, "hostname": {},
}

var forbiddenExternalHostKeyPrefixes = []string{
	"provider", "account", "datacenter", "region", "zone", "image", "size",
	"nativeresource", "resourceid", "resourcegroup", "serverid", "instanceid", "machineid",
	"credential", "ownership", "cleanup", "publicaddress", "privateaddress", "managementaddress", "ipaddress",
}

var externalHostReferencePatterns = map[string]*regexp.Regexp{
	"host-binding":      regexp.MustCompile(`^host-binding://sha256/[a-f0-9]{64}$`),
	"host":              regexp.MustCompile(`^host://sha256/[a-f0-9]{64}$`),
	"host-inventory":    regexp.MustCompile(`^host-inventory://sha256/[a-f0-9]{64}$`),
	"execution-channel": regexp.MustCompile(`^execution-channel://sha256/[a-f0-9]{64}$`),
	"host-conformance":  regexp.MustCompile(`^host-conformance://sha256/[a-f0-9]{64}$`),
}

var (
	externalHostContractIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	externalHostContentHashPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	externalHostSecretRefPattern   = regexp.MustCompile(`^(secret|vault|doppler|techstack)://[^\s]+$`)
	externalHostSemVerPattern      = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

// canonicalInventoryHash binds the observed host facts while excluding the two
// self-referential evidence envelopes. The binding itself carries this hash and
// is separately covered by bindingHash; including it here would make the
// inventory digest cyclic and impossible to issue.
func canonicalInventoryHash(inventory map[string]any) (string, error) {
	clone, err := cloneObject(inventory, true)
	if err != nil {
		return "", fmt.Errorf("clone inventory for hash: %w", err)
	}
	nodes, err := objectField(clone, "inventory", "nodes")
	if err != nil {
		return "", err
	}
	for _, nodeRef := range sortedStringMapKeys(nodes) {
		node, err := asObject(nodes[nodeRef], "inventory.nodes."+nodeRef)
		if err != nil {
			return "", err
		}
		delete(node, "externalHostBinding")
		delete(node, "hostConformanceReceipt")
	}
	return canonicalHash(clone, true)
}

// ComputeExternalHostBindingHash returns the canonical digest of a normalized
// ExternalHostBinding with bindingHash omitted. It performs no provider action.
func ComputeExternalHostBindingHash(binding ExternalHostBinding) (string, error) {
	clone, err := cloneObject(map[string]any(binding), false)
	if err != nil {
		return "", fmt.Errorf("clone external host binding: %w", err)
	}
	delete(clone, "bindingHash")
	return canonicalHash(clone, false)
}

// ComputeHostConformanceReceiptDigest returns the canonical digest of a
// normalized HostConformanceReceipt with receiptDigest omitted.
func ComputeHostConformanceReceiptDigest(receipt HostConformanceReceipt) (string, error) {
	clone, err := cloneObject(map[string]any(receipt), false)
	if err != nil {
		return "", fmt.Errorf("clone host conformance receipt: %w", err)
	}
	delete(clone, "receiptDigest")
	return canonicalHash(clone, false)
}

// ValidateExternalHostBindingFreshness is the deterministic apply-time
// staleness check. The compiler deliberately does not read wall-clock time;
// the caller supplies the execution instant that will be recorded in evidence.
func ValidateExternalHostBindingFreshness(binding ExternalHostBinding, at time.Time) error {
	return validateExternalHostBindingFreshness(binding, "externalHostBinding", at)
}

// ValidateExternalHostBindingForReceipt validates the closed, provider-free
// handoff envelope before an on-host receipt producer trusts it. Intent and
// inventory equality are revalidated later by the final plan compiler.
func ValidateExternalHostBindingForReceipt(binding ExternalHostBinding, at time.Time) error {
	path := "externalHostBinding"
	object := map[string]any(binding)
	if err := validateReceiptBindingEnvelope(object, path); err != nil {
		return err
	}
	if err := validateReceiptBindingCandidate(object, path); err != nil {
		return err
	}
	if err := validateReceiptBindingSecrets(object, path); err != nil {
		return err
	}
	if err := validateReceiptBindingWindowAndHash(binding, path); err != nil {
		return err
	}
	return validateExternalHostBindingFreshness(binding, path, at)
}

func validateReceiptBindingEnvelope(binding map[string]any, path string) error {
	allowedFields := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "bindingRef": {}, "stackId": {}, "nodeRef": {},
		"hostRef": {}, "inventoryRef": {}, "executionChannelRef": {}, "secretRefs": {},
		"stackkitsVersion": {}, "candidateDigest": {}, "specHash": {}, "hostRequirementsHash": {},
		"inventoryHash": {}, "issuedAt": {}, "validUntil": {}, "bindingHash": {},
	}
	for field := range binding {
		if _, allowed := allowedFields[field]; !allowed {
			return fail(ErrContractConflict, path+"."+field, "field is not part of the closed external host binding contract")
		}
	}
	if err := validateProviderFreeHostObject(binding, path); err != nil {
		return err
	}
	if binding["apiVersion"] != externalHostBindingAPIVersion || binding["kind"] != "ExternalHostBinding" {
		return fail(ErrContractConflict, path+".apiVersion", "unsupported external host binding contract")
	}
	for field, scheme := range map[string]string{
		"bindingRef": "host-binding", "hostRef": "host", "inventoryRef": "host-inventory", "executionChannelRef": "execution-channel",
	} {
		if err := validateExternalHostReference(binding, path, field, scheme); err != nil {
			return err
		}
	}
	for _, field := range []string{"stackId", "nodeRef"} {
		value, err := stringField(binding, path, field)
		if err != nil {
			return err
		}
		if !externalHostContractIDPattern.MatchString(value) {
			return fail(ErrContractConflict, path+"."+field, "must be a canonical contract ID")
		}
	}
	return nil
}

func validateReceiptBindingCandidate(binding map[string]any, path string) error {
	version, err := stringField(binding, path, "stackkitsVersion")
	if err != nil {
		return err
	}
	if !externalHostSemVerPattern.MatchString(version) {
		return fail(ErrContractConflict, path+".stackkitsVersion", "must be canonical SemVer")
	}
	for _, field := range []string{"candidateDigest", "specHash", "hostRequirementsHash", "inventoryHash", "bindingHash"} {
		value, err := stringField(binding, path, field)
		if err != nil {
			return err
		}
		if !externalHostContentHashPattern.MatchString(value) {
			return fail(ErrContractConflict, path+"."+field, "must be sha256:<64 lowercase hex>")
		}
	}
	return nil
}

func validateReceiptBindingSecrets(binding map[string]any, path string) error {
	secretRefs, err := objectField(binding, path, "secretRefs")
	if err != nil {
		return err
	}
	for _, ref := range sortedStringMapKeys(secretRefs) {
		value, ok := secretRefs[ref].(string)
		if !externalHostContractIDPattern.MatchString(ref) || !ok || !externalHostSecretRefPattern.MatchString(value) {
			return fail(ErrContractConflict, path+".secretRefs."+ref, "must map canonical IDs to opaque secret references")
		}
	}
	return nil
}

func validateReceiptBindingWindowAndHash(binding ExternalHostBinding, path string) error {
	object := map[string]any(binding)
	issuedAt, err := externalHostTimestamp(object, path, "issuedAt")
	if err != nil {
		return err
	}
	validUntil, err := externalHostTimestamp(object, path, "validUntil")
	if err != nil {
		return err
	}
	if !issuedAt.Before(validUntil) {
		return fail(ErrContractConflict, path+".validUntil", "must be after issuedAt")
	}
	wantHash, err := ComputeExternalHostBindingHash(binding)
	if err != nil {
		return err
	}
	if binding["bindingHash"] != wantHash {
		return fail(ErrContractConflict, path+".bindingHash", "declared binding hash does not match canonical binding body")
	}
	return nil
}

func validateExternalHostBindingFreshness(binding ExternalHostBinding, path string, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, path+".validUntil", "execution time is required")
	}
	issuedAt, err := externalHostTimestamp(map[string]any(binding), path, "issuedAt")
	if err != nil {
		return err
	}
	validUntil, err := externalHostTimestamp(map[string]any(binding), path, "validUntil")
	if err != nil {
		return err
	}
	if at.Before(issuedAt) {
		return fail(ErrExternalHostBindingStale, path+".issuedAt", "binding is not valid before %s", issuedAt.Format(time.RFC3339Nano))
	}
	if !at.Before(validUntil) {
		return fail(ErrExternalHostBindingStale, path+".validUntil", "binding expired at %s", validUntil.Format(time.RFC3339Nano))
	}
	return nil
}

// ValidateExternalHostBindingsFreshness applies the execution-time validity
// decision to every provider-free host handoff in an already verified plan.
// Empty bindings are valid for plans that execute entirely on the current
// host. The caller owns the execution instant so the same value can be written
// to apply evidence; the compiler remains deterministic and clock-free.
func ValidateExternalHostBindingsFreshness(plan ResolvedPlan, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, "resolvedPlan.externalHostBindings", "execution time is required")
	}
	bindings, err := objectField(map[string]any(plan), "resolvedPlan", "externalHostBindings")
	if err != nil {
		return err
	}
	for _, nodeRef := range sortedStringMapKeys(bindings) {
		binding, err := asObject(bindings[nodeRef], "resolvedPlan.externalHostBindings."+nodeRef)
		if err != nil {
			return err
		}
		if err := validateExternalHostBindingFreshness(ExternalHostBinding(binding), "resolvedPlan.externalHostBindings."+nodeRef, at); err != nil {
			return err
		}
	}
	return nil
}

// ValidateHostConformanceReceiptsForApply admits external hosts only when each
// binding has one fresh, identity-bound and conformant StackKits receipt. Plans
// without external host bindings remain valid for local execution.
func ValidateHostConformanceReceiptsForApply(plan ResolvedPlan, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, "resolvedPlan.hostConformanceReceipts", "execution time is required")
	}
	if err := ValidateExternalHostBindingsFreshness(plan, at); err != nil {
		return err
	}
	if err := ValidateExternalHomeAccessBindingsFreshness(plan, at); err != nil {
		return err
	}
	if err := ValidateExternalBackupTargetBindingsFreshness(plan, at); err != nil {
		return err
	}
	if err := ValidateExternalHomeBackupTargetBindingsFreshness(plan, at); err != nil {
		return err
	}
	if err := ValidateExternalFederationLinkBindingsFreshness(plan, at); err != nil {
		return err
	}
	planObject := map[string]any(plan)
	bindings, err := objectField(planObject, "resolvedPlan", "externalHostBindings")
	if err != nil {
		return err
	}
	receipts, err := objectField(planObject, "resolvedPlan", "hostConformanceReceipts")
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	stackID, err := stringField(planObject, "resolvedPlan", "stackId")
	if err != nil {
		return err
	}
	for _, nodeRef := range sortedStringMapKeys(bindings) {
		binding, err := asObject(bindings[nodeRef], "resolvedPlan.externalHostBindings."+nodeRef)
		if err != nil {
			return err
		}
		receiptValue, exists := receipts[nodeRef]
		if !exists {
			return fail(ErrHostConformanceReceiptMissing, "resolvedPlan.hostConformanceReceipts."+nodeRef, "external host binding requires one StackKits host conformance receipt")
		}
		receipt, err := asObject(receiptValue, "resolvedPlan.hostConformanceReceipts."+nodeRef)
		if err != nil {
			return err
		}
		path := "resolvedPlan.hostConformanceReceipts." + nodeRef
		if err := validateHostConformanceReceipt(receipt, binding, nil, path, stackID, nodeRef); err != nil {
			return err
		}
		observedAt, err := externalHostTimestamp(receipt, path, "observedAt")
		if err != nil {
			return err
		}
		validUntil, err := externalHostTimestamp(receipt, path, "validUntil")
		if err != nil {
			return err
		}
		if at.Before(observedAt) {
			return fail(ErrHostConformanceReceiptStale, path+".observedAt", "receipt is not valid before %s", observedAt.Format(time.RFC3339Nano))
		}
		if !at.Before(validUntil) {
			return fail(ErrHostConformanceReceiptStale, path+".validUntil", "receipt expired at %s", validUntil.Format(time.RFC3339Nano))
		}
		result, err := stringField(receipt, path, "result")
		if err != nil {
			return err
		}
		if result != "conformant" {
			return fail(ErrHostConformanceReceiptRejected, path+".result", "apply requires a conformant host receipt, got %q", result)
		}
	}
	return nil
}

// ComputeExternalHostRequirementsHash returns the exact provider-free host
// requirements digest for one node in a canonical plan. Binding issuers use
// this value without learning or transferring any server-provider identity.
func ComputeExternalHostRequirementsHash(plan ResolvedPlan, nodeRef string) (string, error) {
	planObject := map[string]any(plan)
	system, err := objectField(planObject, "resolvedPlan", "system")
	if err != nil {
		return "", err
	}
	nodeValues, err := objectListField(planObject, "resolvedPlan", "nodes")
	if err != nil {
		return "", err
	}
	nodes, err := indexObjectsByID(nodeValues, "resolvedPlan.nodes")
	if err != nil {
		return "", err
	}
	node, exists := nodes[nodeRef]
	if !exists {
		return "", fail(ErrProfileMismatch, "resolvedPlan.nodes", "node %q does not exist", nodeRef)
	}
	return externalHostRequirementsHash(node, system)
}

func buildExternalHostProjection(spec *specView, specHash, inventoryHash string, resolvedSystem map[string]any) (map[string]any, map[string]any, error) {
	bindings := map[string]any{}
	receipts := map[string]any{}
	inventoryNodes, err := objectField(spec.originalInventory, "inventory", "nodes")
	if err != nil {
		return nil, nil, err
	}
	for _, nodeRef := range sortedStringMapKeys(inventoryNodes) {
		facts, err := asObject(inventoryNodes[nodeRef], "inventory.nodes."+nodeRef)
		if err != nil {
			return nil, nil, err
		}
		binding, hasBinding, err := optionalObjectField(facts, "inventory.nodes."+nodeRef, "externalHostBinding")
		if err != nil {
			return nil, nil, err
		}
		receipt, hasReceipt, err := optionalObjectField(facts, "inventory.nodes."+nodeRef, "hostConformanceReceipt")
		if err != nil {
			return nil, nil, err
		}
		if hasReceipt && !hasBinding {
			return nil, nil, fail(ErrContractConflict, "inventory.nodes."+nodeRef+".hostConformanceReceipt.bindingRef", "host conformance requires an external host binding for the same node")
		}
		if !hasBinding {
			continue
		}
		node, exists := spec.nodeByID[nodeRef]
		if !exists {
			return nil, nil, fail(ErrProfileMismatch, "inventory.nodes."+nodeRef+".externalHostBinding.nodeRef", "binding references a node absent from the StackSpec")
		}
		requirementsHash, err := externalHostRequirementsHash(node.object, resolvedSystem)
		if err != nil {
			return nil, nil, err
		}
		if err := validateExternalHostBinding(binding, "inventory.nodes."+nodeRef+".externalHostBinding", spec.stackID, nodeRef, specHash, inventoryHash, requirementsHash); err != nil {
			return nil, nil, err
		}
		bindingClone, err := cloneObject(binding, false)
		if err != nil {
			return nil, nil, err
		}
		bindings[nodeRef] = bindingClone
		if !hasReceipt {
			continue
		}
		if err := validateHostConformanceReceipt(receipt, binding, facts, "inventory.nodes."+nodeRef+".hostConformanceReceipt", spec.stackID, nodeRef); err != nil {
			return nil, nil, err
		}
		receiptClone, err := cloneObject(receipt, false)
		if err != nil {
			return nil, nil, err
		}
		receipts[nodeRef] = receiptClone
	}
	return bindings, receipts, nil
}

func validateExternalHostPlanProjection(plan ResolvedPlan) error {
	planObject := map[string]any(plan)
	stackID, err := stringField(planObject, "resolvedPlan", "stackId")
	if err != nil {
		return err
	}
	specHash, err := stringField(planObject, "resolvedPlan", "specHash")
	if err != nil {
		return err
	}
	inventoryHash, err := stringField(planObject, "resolvedPlan", "inventoryHash")
	if err != nil {
		return err
	}
	system, err := objectField(planObject, "resolvedPlan", "system")
	if err != nil {
		return err
	}
	nodeValues, err := objectListField(planObject, "resolvedPlan", "nodes")
	if err != nil {
		return err
	}
	nodes, err := indexObjectsByID(nodeValues, "resolvedPlan.nodes")
	if err != nil {
		return err
	}
	bindings, err := objectField(planObject, "resolvedPlan", "externalHostBindings")
	if err != nil {
		return err
	}
	receipts, err := objectField(planObject, "resolvedPlan", "hostConformanceReceipts")
	if err != nil {
		return err
	}
	for _, nodeRef := range sortedStringMapKeys(bindings) {
		node, exists := nodes[nodeRef]
		if !exists {
			return fmt.Errorf("resolvedPlan.externalHostBindings.%s has no resolved node", nodeRef)
		}
		requirementsHash, err := externalHostRequirementsHash(node, system)
		if err != nil {
			return err
		}
		binding, err := asObject(bindings[nodeRef], "resolvedPlan.externalHostBindings."+nodeRef)
		if err != nil {
			return err
		}
		if err := validateExternalHostBinding(binding, "resolvedPlan.externalHostBindings."+nodeRef, stackID, nodeRef, specHash, inventoryHash, requirementsHash); err != nil {
			return err
		}
	}
	for _, nodeRef := range sortedStringMapKeys(receipts) {
		binding, exists := bindings[nodeRef]
		if !exists {
			return fmt.Errorf("resolvedPlan.hostConformanceReceipts.%s has no external host binding", nodeRef)
		}
		bindingObject, err := asObject(binding, "resolvedPlan.externalHostBindings."+nodeRef)
		if err != nil {
			return err
		}
		receipt, err := asObject(receipts[nodeRef], "resolvedPlan.hostConformanceReceipts."+nodeRef)
		if err != nil {
			return err
		}
		if err := validateHostConformanceReceipt(receipt, bindingObject, nil, "resolvedPlan.hostConformanceReceipts."+nodeRef, stackID, nodeRef); err != nil {
			return err
		}
	}
	return nil
}

func validateExternalHostBinding(binding map[string]any, path, stackID, nodeRef, specHash, inventoryHash, requirementsHash string) error {
	if err := validateProviderFreeHostObject(binding, path); err != nil {
		return err
	}
	if binding["apiVersion"] != externalHostBindingAPIVersion || binding["kind"] != "ExternalHostBinding" {
		return fail(ErrContractConflict, path+".apiVersion", "unsupported external host binding contract")
	}
	for field, scheme := range map[string]string{
		"bindingRef": "host-binding", "hostRef": "host", "inventoryRef": "host-inventory", "executionChannelRef": "execution-channel",
	} {
		if err := validateExternalHostReference(binding, path, field, scheme); err != nil {
			return err
		}
	}
	for _, expected := range []struct {
		field string
		value string
	}{
		{field: "stackId", value: stackID},
		{field: "nodeRef", value: nodeRef},
		{field: "specHash", value: specHash},
		{field: "inventoryHash", value: inventoryHash},
		{field: "hostRequirementsHash", value: requirementsHash},
	} {
		have, err := stringField(binding, path, expected.field)
		if err != nil {
			return err
		}
		if have != expected.value {
			return fail(ErrProfileMismatch, path+"."+expected.field, "binding value %q does not match %q", have, expected.value)
		}
	}
	issuedAt, err := externalHostTimestamp(binding, path, "issuedAt")
	if err != nil {
		return err
	}
	validUntil, err := externalHostTimestamp(binding, path, "validUntil")
	if err != nil {
		return err
	}
	if !issuedAt.Before(validUntil) {
		return fail(ErrContractConflict, path+".validUntil", "must be after issuedAt")
	}
	wantHash, err := ComputeExternalHostBindingHash(ExternalHostBinding(binding))
	if err != nil {
		return err
	}
	haveHash, err := stringField(binding, path, "bindingHash")
	if err != nil {
		return err
	}
	if haveHash != wantHash {
		return fail(ErrContractConflict, path+".bindingHash", "declared binding hash does not match canonical binding body")
	}
	return nil
}

func validateHostConformanceReceipt(receipt, binding, inventoryFacts map[string]any, path, stackID, nodeRef string) error {
	if err := validateProviderFreeHostObject(receipt, path); err != nil {
		return err
	}
	if receipt["apiVersion"] != hostConformanceReceiptAPIVersion || receipt["kind"] != "HostConformanceReceipt" {
		return fail(ErrContractConflict, path+".apiVersion", "unsupported host conformance receipt contract")
	}
	if err := validateHostConformanceIdentity(receipt, binding, path, stackID, nodeRef); err != nil {
		return err
	}
	if err := validateHostConformanceWindow(receipt, binding, path); err != nil {
		return err
	}
	wantDigest, err := ComputeHostConformanceReceiptDigest(HostConformanceReceipt(receipt))
	if err != nil {
		return err
	}
	haveDigest, err := stringField(receipt, path, "receiptDigest")
	if err != nil {
		return err
	}
	if haveDigest != wantDigest {
		return fail(ErrContractConflict, path+".receiptDigest", "declared receipt digest does not match canonical receipt body")
	}
	return validateHostConformanceInventoryFacts(receipt, inventoryFacts, path)
}

func validateHostConformanceIdentity(receipt, binding map[string]any, path, stackID, nodeRef string) error {
	if err := validateExternalHostReference(receipt, path, "receiptRef", "host-conformance"); err != nil {
		return err
	}
	if err := validateExternalHostReference(receipt, path, "bindingRef", "host-binding"); err != nil {
		return err
	}
	bindingRef, err := stringField(binding, path+".binding", "bindingRef")
	if err != nil {
		return err
	}
	bindingHash, err := stringField(binding, path+".binding", "bindingHash")
	if err != nil {
		return err
	}
	stackKitsVersion, err := stringField(binding, path+".binding", "stackkitsVersion")
	if err != nil {
		return err
	}
	candidateDigest, err := stringField(binding, path+".binding", "candidateDigest")
	if err != nil {
		return err
	}
	for _, expected := range []struct {
		field string
		value string
	}{
		{field: "stackId", value: stackID},
		{field: "nodeRef", value: nodeRef},
		{field: "bindingRef", value: bindingRef},
		{field: "bindingHash", value: bindingHash},
		{field: "stackkitsVersion", value: stackKitsVersion},
		{field: "candidateDigest", value: candidateDigest},
	} {
		have, err := stringField(receipt, path, expected.field)
		if err != nil {
			return err
		}
		if have != expected.value {
			return fail(ErrProfileMismatch, path+"."+expected.field, "receipt value %q does not match %q", have, expected.value)
		}
	}
	return nil
}

func validateHostConformanceWindow(receipt, binding map[string]any, path string) error {
	issuedAt, err := externalHostTimestamp(binding, path+".binding", "issuedAt")
	if err != nil {
		return err
	}
	bindingValidUntil, err := externalHostTimestamp(binding, path+".binding", "validUntil")
	if err != nil {
		return err
	}
	observedAt, err := externalHostTimestamp(receipt, path, "observedAt")
	if err != nil {
		return err
	}
	receiptValidUntil, err := externalHostTimestamp(receipt, path, "validUntil")
	if err != nil {
		return err
	}
	if observedAt.Before(issuedAt) || !observedAt.Before(receiptValidUntil) || receiptValidUntil.After(bindingValidUntil) {
		return fail(ErrContractConflict, path+".validUntil", "receipt observation window must be contained by the external host binding window")
	}
	return nil
}

func validateHostConformanceInventoryFacts(receipt, inventoryFacts map[string]any, path string) error {
	if inventoryFacts == nil {
		return nil
	}
	facts, err := objectField(receipt, path, "facts")
	if err != nil {
		return err
	}
	if arch, exists, err := optionalStringField(inventoryFacts, path+".inventory", "arch"); err != nil {
		return err
	} else if exists && facts["architecture"] != arch {
		return fail(ErrProfileMismatch, path+".facts.architecture", "receipt architecture does not match inventory fact")
	}
	virtualization, exists, err := optionalStringField(inventoryFacts, path+".inventory", "virtualization")
	if err != nil || !exists {
		return err
	}
	virtualizationFacts, err := objectField(facts, path+".facts", "virtualization")
	if err != nil {
		return err
	}
	if virtualizationFacts["class"] != virtualization {
		return fail(ErrProfileMismatch, path+".facts.virtualization.class", "receipt virtualization does not match inventory fact")
	}
	return nil
}

func externalHostRequirementsHash(node, system map[string]any) (string, error) {
	hardware, err := objectField(node, "node", "hardware")
	if err != nil {
		return "", err
	}
	hardwareClone, err := cloneObject(hardware, false)
	if err != nil {
		return "", err
	}
	systemClone, err := cloneObject(system, false)
	if err != nil {
		return "", err
	}
	nodeRef, err := stringField(node, "node", "id")
	if err != nil {
		return "", err
	}
	return canonicalHash(map[string]any{"nodeRef": nodeRef, "hardware": hardwareClone, "system": systemClone}, false)
}

func validateExternalHostReference(object map[string]any, path, field, scheme string) error {
	value, err := stringField(object, path, field)
	if err != nil {
		return err
	}
	pattern, ok := externalHostReferencePatterns[scheme]
	if !ok {
		return fmt.Errorf("unknown external host reference scheme %q", scheme)
	}
	if !pattern.MatchString(value) {
		return fail(ErrContractConflict, path+"."+field, "must use the opaque %s://sha256/<64 lowercase hex> reference grammar", scheme)
	}
	return nil
}

func externalHostTimestamp(object map[string]any, path, field string) (time.Time, error) {
	value, err := stringField(object, path, field)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fail(ErrContractConflict, path+"."+field, "must be a canonical RFC3339Nano UTC timestamp")
	}
	return parsed, nil
}

func validateProviderFreeHostObject(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
			if isForbiddenExternalHostKey(normalized) {
				return fail(ErrContractConflict, path+"."+key, "server-provider and resource-lifecycle fields are forbidden in StackKits host contracts")
			}
			if err := validateProviderFreeHostObject(nested, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, nested := range typed {
			if err := validateProviderFreeHostObject(nested, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func isForbiddenExternalHostKey(normalized string) bool {
	if _, forbidden := forbiddenExternalHostKeys[normalized]; forbidden {
		return true
	}
	for _, prefix := range forbiddenExternalHostKeyPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
