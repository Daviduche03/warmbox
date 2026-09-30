package api

import (
	"net/http"
	"strconv"

	"warmbox/internal/catalog"
)

// SettingsResponse is what the dashboard's Storage tab reads.
type SettingsResponse struct {
	// MaxDesktopsPerWorkspace is the configured cap; 0 means unlimited.
	MaxDesktopsPerWorkspace int `json:"max_desktops_per_workspace"`
	// DesktopsInWorkspace is the caller's current usage, so the tab can show
	// "2 of 5".
	DesktopsInWorkspace int `json:"desktops_in_workspace"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	ws, _ := s.callerScope(r)
	n := 0
	for _, info := range s.mgr.List() {
		if info.Workspace != "" && info.Workspace == ws {
			n++
		}
	}
	writeJSON(w, http.StatusOK, SettingsResponse{
		MaxDesktopsPerWorkspace: s.desktopLimit(),
		DesktopsInWorkspace:     n,
	})
}

// updateSettings changes daemon-wide policy. Only admins reach it (see
// minRoleFor): the desktop limit is what keeps one workspace from eating the
// host's RAM.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "settings store unavailable"})
		return
	}
	var req struct {
		MaxDesktopsPerWorkspace *int `json:"max_desktops_per_workspace"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.MaxDesktopsPerWorkspace != nil {
		n := *req.MaxDesktopsPerWorkspace
		if n < 0 || n > maxDesktopsLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "the desktop limit must be between 0 (unlimited) and 1000",
			})
			return
		}
		if err := s.catalog.SetSetting(catalog.SettingMaxDesktopsPerWorkspace, strconv.Itoa(n)); err != nil {
			s.fail(w, http.StatusInternalServerError, "could not save settings", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"max_desktops_per_workspace": s.desktopLimit(),
	})
}

// maxDesktopsLimit is the largest cap the UI may set (0 disables the cap).
const maxDesktopsLimit = 1000
