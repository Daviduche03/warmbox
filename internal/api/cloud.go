package api

import (
	"net/http"
	"strings"

	"warmbox/internal/config"
)

// The cloud remote is a host-level setting — it decides where volume bytes live
// on this machine — so it is owner-only and stored in ~/.warmbox/config.json
// rather than in the per-workspace database. This is the same file the
// `warmbox cloud` command writes.
//
// Changing it can't take effect until the daemon rebuilds its volume store, so
// writes report restart_required and the UI says so.

type cloudView struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider"`
	Bucket     string `json:"bucket"`
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region"`
	AccessKey  string `json:"access_key"` // masked
	SecretKey  string `json:"secret_key"` // masked
	Path       string `json:"path"`
}

// maskSecret keeps enough to recognise a key without ever returning it whole.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "••••"
	}
	return s[:4] + "••••••"
}

func (s *Server) cloudGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadGlobal()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p, _ := config.GlobalPath()
	view := cloudView{Path: p}
	if cfg.Configured() {
		view.Configured = true
		view.Provider = cfg.Provider
		view.Bucket = cfg.DefaultBucket
		view.Endpoint = cfg.Endpoint
		view.Region = cfg.Region
		view.AccessKey = maskSecret(cfg.AccessKey)
		view.SecretKey = maskSecret(cfg.SecretKey)
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) cloudPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider  string `json:"provider"`
		Bucket    string `json:"bucket"`
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req.Bucket = strings.TrimSpace(req.Bucket)
	if req.Bucket == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bucket is required"})
		return
	}

	// Load-then-merge so a blank field means "leave it alone" — the UI only
	// ever shows masked keys, so it can't round-trip the real ones.
	cfg, _ := config.LoadGlobal()
	if cfg == nil {
		cfg = &config.RemoteConfig{}
	}
	cfg.DefaultBucket = req.Bucket
	if v := strings.TrimSpace(req.Provider); v != "" {
		cfg.Provider = v
	} else if cfg.Provider == "" {
		cfg.Provider = "Cloudflare"
	}
	if v := strings.TrimSpace(req.Endpoint); v != "" {
		cfg.Endpoint = v
	}
	if v := strings.TrimSpace(req.Region); v != "" {
		cfg.Region = v
	}
	if v := strings.TrimSpace(req.AccessKey); v != "" {
		cfg.AccessKey = v
	}
	if v := strings.TrimSpace(req.SecretKey); v != "" {
		cfg.SecretKey = v
	}
	if err := config.SaveGlobal(cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart_required": true})
}

func (s *Server) cloudDelete(w http.ResponseWriter, r *http.Request) {
	if err := config.Clear(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart_required": true})
}
