package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/logbuf"
)

// handleSystemEnv lists the configuration variables and the values in
// effect. Secrets are already withheld by config.Settings.
func (s *Server) handleSystemEnv(w http.ResponseWriter, r *http.Request) {
	settings := s.env
	if settings == nil {
		settings = []config.Setting{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// logsResponse is a page of recent log records. A client that passes the last
// seq it saw gets only newer records. oldest_seq above that seq plus one means
// records were discarded before it could fetch them; latest_seq below it means
// the server has restarted and numbering began again.
type logsResponse struct {
	Records   []logbuf.Record `json:"records"`
	OldestSeq uint64          `json:"oldest_seq"`
	LatestSeq uint64          `json:"latest_seq"`
	Capacity  int             `json:"capacity"`
	Level     string          `json:"level"`
}

// handleSystemLogs returns the buffered log records newer than ?after=<seq>.
func (s *Server) handleSystemLogs(w http.ResponseWriter, r *http.Request) {
	var after uint64
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_value", "after must be a sequence number")
			return
		}
		after = n
	}

	resp := logsResponse{
		Records:  []logbuf.Record{},
		Capacity: logbuf.Capacity,
		Level:    strings.ToLower(config.LogLevel().String()),
	}
	if s.logs != nil {
		page := s.logs.Since(after)
		resp.Records, resp.OldestSeq, resp.LatestSeq = page.Records, page.Oldest, page.Latest
	}
	writeJSON(w, http.StatusOK, resp)
}
