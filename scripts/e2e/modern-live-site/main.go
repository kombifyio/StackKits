package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type state struct {
	Site      string `json:"site"`
	Role      string `json:"role"`
	Sequence  int64  `json:"sequence"`
	UpdatedAt string `json:"updatedAt"`
}

var (
	mu      sync.Mutex
	current = state{Site: os.Getenv("STACKKIT_SITE"), Role: os.Getenv("STACKKIT_ROLE")}
	standby = os.Getenv("STACKKIT_STANDBY_URL")
)

func main() {
	if current.Site == "" || (current.Role != "active" && current.Role != "standby" && current.Role != "edge") {
		panic("STACKKIT_SITE and STACKKIT_ROLE are required")
	}
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { write(w, current) })
	http.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) { mu.Lock(); defer mu.Unlock(); write(w, current) })
	http.HandleFunc("/commit", commit)
	http.HandleFunc("/replicate", replicate)
	http.HandleFunc("/promote", promote)
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}

func commit(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	if current.Role != "active" {
		mu.Unlock()
		http.Error(w, "not active", http.StatusConflict)
		return
	}
	current.Sequence++
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	snapshot := current
	mu.Unlock()
	if standby != "" {
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/replicate?sequence=%d", standby, snapshot.Sequence), nil)
		res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil || res.StatusCode != http.StatusOK {
			if res != nil {
				_ = res.Body.Close()
			}
			http.Error(w, "replication failed", http.StatusBadGateway)
			return
		}
		_ = res.Body.Close()
	}
	write(w, snapshot)
}

func replicate(w http.ResponseWriter, r *http.Request) {
	value, err := strconv.ParseInt(r.URL.Query().Get("sequence"), 10, 64)
	if err != nil || value < 1 {
		http.Error(w, "invalid sequence", http.StatusBadRequest)
		return
	}
	mu.Lock()
	if current.Role != "standby" || value < current.Sequence {
		mu.Unlock()
		http.Error(w, "replication rejected", http.StatusConflict)
		return
	}
	current.Sequence = value
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	snapshot := current
	mu.Unlock()
	write(w, snapshot)
}

func promote(w http.ResponseWriter, _ *http.Request) {
	mu.Lock()
	if current.Role != "standby" {
		mu.Unlock()
		http.Error(w, "not standby", http.StatusConflict)
		return
	}
	current.Role = "active"
	current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	snapshot := current
	mu.Unlock()
	write(w, snapshot)
}

func write(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
