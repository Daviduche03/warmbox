package api

import (
	"fmt"
	"net/http"
	"time"
)

// StatusInfo is the daemon-level detail the API reports to the dashboard. It is
// supplied by the caller (cmd/warmbox) because it depends on how the daemon was
// launched — which backend, which storage, and so on.
type StatusInfo struct {
	Version       string
	Backend       string
	VolumesBacked string
	DefaultImage  string
}

// SetStatusInfo records the daemon description surfaced by GET /api/status.
func (s *Server) SetStatusInfo(i StatusInfo) { s.info = i }

// StatusResponse is the shape the dashboard consumes.
type StatusResponse struct {
	Version       string   `json:"version"`
	Backend       string   `json:"backend"`
	Addr          string   `json:"addr"`
	TokenRequired bool     `json:"token_required"`
	Up            string   `json:"up"`
	Pool          poolInfo `json:"pool"`
	Desktops      int      `json:"desktops"`
	Volumes       int      `json:"volumes"`
	Snapshots     int      `json:"snapshots"`
	Images        []string `json:"images"`
	VolumesBacked string   `json:"volumes_backed"`
	DefaultImage  string   `json:"default_image"`
}

type poolInfo struct {
	Size    int `json:"size"`
	Idle    int `json:"idle"`
	Booting int `json:"booting"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	idle, pending := s.pool.Stats()
	out := StatusResponse{
		Version:       or(s.info.Version, "dev"),
		Backend:       or(s.info.Backend, "vfkit"),
		Addr:          s.cfg.APIAddr,
		TokenRequired: s.cfg.Token != "",
		Up:            humanDuration(time.Since(s.started)),
		Pool:          poolInfo{Size: s.cfg.PoolSize, Idle: idle, Booting: pending},
		Desktops:      len(s.mgr.List()),
		Images:        s.mgr.Images(),
		VolumesBacked: or(s.info.VolumesBacked, "local storage"),
		DefaultImage:  or(s.info.DefaultImage, s.cfg.Image),
	}
	if out.Images == nil {
		out.Images = []string{}
	}
	if s.catalog != nil {
		if vs, err := s.catalog.ListVolumes(); err == nil {
			out.Volumes = len(vs)
		}
		if sn, err := s.catalog.ListSnapshots(""); err == nil {
			out.Snapshots = len(sn)
		}
	} else if s.volumes != nil {
		if vs, err := s.volumes.List(r.Context()); err == nil {
			out.Volumes = len(vs)
		}
		if sn, err := s.volumes.ListSnapshots(r.Context()); err == nil {
			out.Snapshots = len(sn)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// humanDuration renders a duration compactly: "3d 4h", "5h 12m", "12m 30s", "9s".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int64(d.Seconds())
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm %ds", mins, secs%60)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}
