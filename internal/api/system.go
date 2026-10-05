package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/logbuf"
)

// handleSystemEnv lists the configuration variables and the values in
// effect. Secrets are already withheld by config.Settings.
func (s *Server) handleSystemEnv(w http.ResponseWriter, r *http.Request) {
	settings := s.system.Env
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
	// Level is the live level; ConfiguredLevel is LOG_LEVEL, which the live
	// level returns to on restart.
	Level           string `json:"level"`
	ConfiguredLevel string `json:"configured_level"`
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
		Records:         []logbuf.Record{},
		Capacity:        logbuf.Capacity,
		Level:           levelName(s.system.Level.Level()),
		ConfiguredLevel: levelName(config.LogLevel()),
	}
	if s.system.Logs != nil {
		page := s.system.Logs.Since(after)
		resp.Records, resp.OldestSeq, resp.LatestSeq = page.Records, page.Oldest, page.Latest
	}
	writeJSON(w, http.StatusOK, resp)
}

// logLevels are the levels the System page may choose.
var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// handleSetLogLevel changes the live logging level until the next restart,
// when it returns to LOG_LEVEL.
func (s *Server) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level string `json:"level"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	level, ok := logLevels[strings.ToLower(strings.TrimSpace(req.Level))]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_value", "level must be debug, info, warn or error")
		return
	}

	// Recorded at info while the more verbose of the two levels is in force,
	// so the change shows up in the log whichever way it goes.
	old := s.system.Level.Level()
	if level > old {
		slog.Info("log level changed", "from", levelName(old), "to", levelName(level))
		s.system.Level.Set(level)
	} else {
		s.system.Level.Set(level)
		slog.Info("log level changed", "from", levelName(old), "to", levelName(level))
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "level": levelName(level)})
}

func levelName(l slog.Level) string {
	return strings.ToLower(l.String())
}
