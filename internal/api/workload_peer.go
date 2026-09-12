package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Workload peer management remains on the Home management surface. Neither
// forwarded address headers nor the ordinary API credential grant owner signing
// authority. The dedicated origin TLS listener never installs these routes.
func (s *Server) handleWorkloadPeerOperation(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || s.config.APIKey == "" {
		http.Error(w, "local owner authorization required", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var operation localevidence.WorkloadPeerOperation
	if err := decoder.Decode(&operation); err != nil {
		http.Error(w, "invalid owner operation", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "invalid owner operation", http.StatusBadRequest)
		return
	}
	if err := localevidence.ApplyWorkloadPeerOperation(s.config.BaseDir, operation); err != nil {
		http.Error(w, "owner operation rejected", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
