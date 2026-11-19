package api

import (
	"fmt"
	"os"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

func newStackActionResponse(req stackActionRequest, mode stackaction.Mode) stackActionResponse {
	resp := stackActionResponse{
		Status:      stackaction.StatusAccepted,
		Action:      req.Action,
		StackID:     req.StackID,
		StackName:   strings.TrimSpace(req.StackName),
		StackKit:    strings.TrimSpace(req.StackKit),
		TofuDir:     strings.TrimSpace(req.TofuDir),
		UnifiedPath: strings.TrimSpace(req.UnifiedPath),
		Mode:        mode,
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "request", Status: stackaction.CheckStatusOK, Detail: "StackAction payload decoded"})
	resp.Checks = appendPathCheckStackAction(resp.Checks, "tofu_dir", resp.TofuDir, true)
	resp.Checks = appendPathCheckStackAction(resp.Checks, "unified_path", resp.UnifiedPath, false)
	if resp.StackKit == "" {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit", Status: stackaction.CheckStatusWarning, Detail: "stackkit name not provided"})
		return resp
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit", Status: stackaction.CheckStatusOK, Detail: resp.StackKit})
	return resp
}

func prepareStackActionRequest(resp *stackActionResponse, req *stackActionRequest) (*stackActionTarget, *stackaction.HomelabEnrollment, *skerrors.StackKitError) {
	if req.OwnerSpecRef != nil {
		ownerRef, err := normalizeStackActionReference(req.OwnerSpecRef, stackActionScopeOwnerSpecRead, time.Now())
		if err != nil {
			resp.Status = stackaction.StatusFailed
			return nil, nil, skerrors.NewValidationError(
				"invalid_owner_spec_ref",
				"owner_spec_ref must be versioned, unexpired, and scoped for owner-spec read",
				skerrors.WithField("error", err.Error()),
			)
		}
		req.OwnerSpecRef = ownerRef
	}
	target, targetErr := normalizeStackActionRequestTarget(resp, req)
	if targetErr != nil {
		return nil, nil, targetErr
	}
	enrollment, enrollmentErr := normalizeStackActionRequestEnrollment(resp, req)
	if enrollmentErr != nil {
		return target, nil, enrollmentErr
	}
	if nodeErr := normalizeStackActionRequestPlatformNodes(resp, req); nodeErr != nil {
		return target, enrollment, nodeErr
	}
	return target, enrollment, nil
}

func normalizeStackActionRequestTarget(resp *stackActionResponse, req *stackActionRequest) (*stackActionTarget, *skerrors.StackKitError) {
	target := normalizeStackActionTarget(req.RuntimeTarget)
	if target == nil {
		if req.RuntimeTarget != nil && strings.TrimSpace(req.RuntimeTarget.DockerHost) != "" {
			resp.Status = stackaction.StatusFailed
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: "runtime target host is required"})
			return nil, skerrors.NewValidationError(
				"invalid_runtime_target",
				"runtime_target host is required when docker_host is set",
			)
		}
		return nil, nil
	}
	if _, targetErr := runtimeTargetDockerHostStackAction(target); targetErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: targetErr.Error()})
		return nil, skerrors.NewValidationError(
			"invalid_runtime_target",
			"runtime_target must use the validated SSH Docker transport",
			skerrors.WithField("error", targetErr.Error()),
		)
	}
	accessRef, accessErr := normalizeStackActionReference(target.AccessProfileRef, stackActionScopeRuntimeSSH, time.Now())
	if accessErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: "invalid access_profile_ref"})
		return nil, skerrors.NewValidationError(
			"invalid_runtime_access_profile_ref",
			"runtime_target access_profile_ref must be versioned, unexpired, and scoped for SSH",
			skerrors.WithField("error", accessErr.Error()),
		)
	}
	if hostKeyErr := validateRuntimeTargetHostKeyStackAction(target); hostKeyErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: hostKeyErr.Message})
		return nil, hostKeyErr
	}
	target.AccessProfileRef = accessRef
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: "ok", Detail: target.Host})
	req.RuntimeTarget = target
	return target, nil
}

func normalizeStackActionRequestEnrollment(resp *stackActionResponse, req *stackActionRequest) (*stackaction.HomelabEnrollment, *skerrors.StackKitError) {
	enrollment, enrollmentErr := normalizeHomelabEnrollmentStackAction(req.HomelabEnrollment)
	if enrollmentErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "homelab_enrollment", Status: stackaction.CheckStatusFailed, Detail: enrollmentErr.Error()})
		return nil, skerrors.NewValidationError(
			"homelab_enrollment_incomplete",
			"homelab_enrollment requires lease_id, server_url, server_id, runtime_agent_id, enrollment_access_ref, and heartbeat_url or inventory_url",
			skerrors.WithField("error", enrollmentErr.Error()),
		)
	}
	if enrollment != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "homelab_enrollment", Status: stackaction.CheckStatusOK, Detail: enrollment.RuntimeAgentID})
		req.HomelabEnrollment = enrollment
	}
	return enrollment, nil
}

func normalizeStackActionRequestPlatformNodes(resp *stackActionResponse, req *stackActionRequest) *skerrors.StackKitError {
	nodes, err := normalizeStackActionPlatformNodeReferences(req.PlatformNodes, time.Now())
	if err != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "platform_nodes", Status: stackaction.CheckStatusFailed, Detail: err.Error()})
		return skerrors.NewValidationError(
			"invalid_platform_node_reference",
			"platform_nodes must use observed platform IDs or valid opaque onboarding and access-profile references",
			skerrors.WithField("error", err.Error()),
		)
	}
	if len(nodes) > 0 {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "platform_nodes", Status: stackaction.CheckStatusOK, Detail: fmt.Sprintf("%d supplemental node(s) requested", len(nodes))})
		req.PlatformNodes = nodes
	}
	return nil
}

func requiresManagedRuntimeTargetStackAction(action stackaction.Action, enrollment *stackaction.HomelabEnrollment, target *stackActionTarget) bool {
	if enrollment == nil || target != nil {
		return false
	}
	return action == stackActionRollout || action == stackActionVerify
}

func normalizeStackActionTarget(target *stackActionTarget) *stackActionTarget {
	if target == nil {
		return nil
	}
	normalized := *target
	normalized.Host = firstRuntimeOutputStackAction(map[string]string{
		"host":       normalized.Host,
		"public_ip":  normalized.PublicIP,
		"private_ip": normalized.PrivateIP,
	}, "host", "public_ip", "private_ip")
	normalized.User = strings.TrimSpace(normalized.User)
	if normalized.User == "" {
		normalized.User = "root"
	}
	if normalized.Port <= 0 {
		normalized.Port = 22
	}
	normalized.DockerHost = strings.TrimSpace(normalized.DockerHost)
	if normalized.Host == "" {
		return nil
	}
	return &normalized
}

func normalizeHomelabEnrollmentStackAction(enrollment *stackaction.HomelabEnrollment) (*stackaction.HomelabEnrollment, error) {
	if enrollment == nil {
		return nil, nil
	}
	normalized := *enrollment
	normalized.TenantID = strings.TrimSpace(normalized.TenantID)
	normalized.OwnerID = strings.TrimSpace(normalized.OwnerID)
	normalized.StackID = strings.TrimSpace(normalized.StackID)
	normalized.LeaseID = strings.TrimSpace(normalized.LeaseID)
	normalized.ServerURL = strings.TrimRight(strings.TrimSpace(normalized.ServerURL), "/")
	normalized.ServerID = strings.TrimSpace(normalized.ServerID)
	normalized.RuntimeAgentID = strings.TrimSpace(normalized.RuntimeAgentID)
	normalized.HeartbeatURL = strings.TrimSpace(normalized.HeartbeatURL)
	normalized.InventoryURL = strings.TrimSpace(normalized.InventoryURL)
	missing := []string{}
	for name, value := range map[string]string{
		"lease_id":         normalized.LeaseID,
		"server_url":       normalized.ServerURL,
		"server_id":        normalized.ServerID,
		"runtime_agent_id": normalized.RuntimeAgentID,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if normalized.HeartbeatURL == "" && normalized.InventoryURL == "" {
		missing = append(missing, "heartbeat_url or inventory_url")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	enrollmentRef, err := normalizeStackActionReference(normalized.EnrollmentAccessRef, stackActionScopeRuntimeEnroll, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid enrollment_access_ref: %w", err)
	}
	normalized.EnrollmentAccessRef = enrollmentRef
	return &normalized, nil
}

func appendPathCheckStackAction(checks []stackActionCheck, name, path string, wantDir bool) []stackActionCheck {
	path = strings.TrimSpace(path)
	if path == "" {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusMissing})
	}
	info, err := os.Stat(path)
	if err != nil {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusReference, Detail: path})
	}
	if wantDir && !info.IsDir() {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusWarning, Detail: "path is not a directory"})
	}
	if !wantDir && info.IsDir() {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusWarning, Detail: "path is a directory"})
	}
	return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusOK, Detail: path})
}
