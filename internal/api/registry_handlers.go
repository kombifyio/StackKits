package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
)

type registryResponse struct {
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

func (s *Server) handleRegisterInstance(w http.ResponseWriter, r *http.Request) {
	var reg models.InstanceRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON format")
		return
	}
	if reg.InstanceID == "" {
		writeError(w, r, http.StatusBadRequest, "instance_id field is required")
		return
	}
	if reg.EndpointURL == "" {
		writeError(w, r, http.StatusBadRequest, "endpoint_url field is required")
		return
	}
	if reg.StackKit == "" {
		writeError(w, r, http.StatusBadRequest, "stackkit field is required")
		return
	}
	if reg.Status == "" {
		writeError(w, r, http.StatusBadRequest, "status field is required")
		return
	}
	if reg.LastSeen.IsZero() {
		reg.LastSeen = time.Now().UTC()
	}

	s.registryMu.Lock()
	s.registryInstances[reg.InstanceID] = reg
	s.registryMu.Unlock()

	writeSuccess(w, r, http.StatusCreated, registryResponse{
		InstanceID: reg.InstanceID,
		Status:     "registered",
	})
}

func (s *Server) handleRegistryHeartbeat(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	if instanceID == "" {
		writeError(w, r, http.StatusBadRequest, "instance ID is required")
		return
	}

	var body struct {
		InstanceID string `json:"instance_id"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON format")
		return
	}
	if body.InstanceID != "" && body.InstanceID != instanceID {
		writeError(w, r, http.StatusBadRequest, "instance_id must match path")
		return
	}
	if body.Status == "" {
		writeError(w, r, http.StatusBadRequest, "status field is required")
		return
	}

	s.registryMu.Lock()
	reg, ok := s.registryInstances[instanceID]
	if ok {
		reg.Status = body.Status
		reg.LastSeen = time.Now().UTC()
		s.registryInstances[instanceID] = reg
	}
	s.registryMu.Unlock()
	if !ok {
		writeError(w, r, http.StatusNotFound, "registry instance not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeregisterInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	if instanceID == "" {
		writeError(w, r, http.StatusBadRequest, "instance ID is required")
		return
	}

	s.registryMu.Lock()
	_, ok := s.registryInstances[instanceID]
	if ok {
		delete(s.registryInstances, instanceID)
	}
	s.registryMu.Unlock()
	if !ok {
		writeError(w, r, http.StatusNotFound, "registry instance not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
