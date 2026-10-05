// Package api serves the REST API, the WebSocket endpoint and the browser UI.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/keg"
	"github.com/matt-freed/open-plaato-keg/internal/logbuf"
	"github.com/matt-freed/open-plaato-keg/internal/store"
	"github.com/matt-freed/open-plaato-keg/internal/ws"
)

// Server wires the HTTP handlers to the rest of the application.
type Server struct {
	store     *store.Store
	commander *keg.Commander
	hub       *ws.Hub
	bus       *events.Bus
	version   string
	static    fs.FS
	system    System
}

// System is what the System page reads and controls.
type System struct {
	// Logs holds the recent log records.
	Logs *logbuf.Buffer
	// Level is the live logging level, which the page can change until the
	// next restart.
	Level *slog.LevelVar
	// Env is the configuration in effect, with secrets withheld.
	Env []config.Setting
}

// NewServer returns a configured API server.
func NewServer(st *store.Store, cmd *keg.Commander, hub *ws.Hub, bus *events.Bus,
	version string, static fs.FS, sys System) *Server {
	if sys.Level == nil {
		sys.Level = new(slog.LevelVar)
	}
	return &Server{
		store: st, commander: cmd, hub: hub, bus: bus,
		version: version, static: static, system: sys,
	}
}

// Handler builds the HTTP routing tree.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	r.Route("/api", func(r chi.Router) {
		r.Get("/alive", s.handleAlive)

		// Reads are GET. A change to a resource is PUT when the body replaces
		// it, PATCH when it carries only what changes, and DELETE to remove
		// it; the bulk forms name their targets in the body. POST creates a
		// tap or sends a keg a command.
		r.Route("/kegs", func(r chi.Router) {
			// Declared before /{id} so they are not swallowed by it.
			r.Get("/devices", s.handleListKegIDs)
			r.Get("/connected", s.handleListConnected)
			r.Get("/", s.handleListKegs)
			r.Put("/order", s.handleKegOrder)

			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetKeg)
				r.Patch("/", s.handleUpdateKeg)
				r.Delete("/", s.handleDeleteKeg)
				r.Get("/connection", s.handleKegConnection)
				r.Get("/log", s.handleKegLog)
				r.Delete("/log", s.handleClearKegLog)
				r.Get("/log/csv", s.handleKegLogCSV)
				r.Get("/log/rows", s.handleKegLogRows)
				r.Patch("/log/rows", s.handleUpdateKegLog)
				r.Delete("/log/rows", s.handleDeleteKegLogEntries)
				r.Get("/pours", s.handleKegPours)
				s.mountKegCommands(r)
			})
		})

		r.Route("/pours", func(r chi.Router) {
			r.Get("/", s.handleListPours)
			r.Patch("/", s.handleUpdatePours)
			r.Delete("/", s.handleDeletePours)
			r.Get("/csv", s.handleListPoursCSV)
			r.Get("/summary", s.handleSummarizePours)
			r.Delete("/{id}", s.handleDeletePour)
		})

		r.Route("/taps", func(r chi.Router) {
			r.Get("/", s.handleListTaps)
			r.Post("/", s.handleCreateTap)
			// Declared before /{id} so they are not swallowed by it.
			r.Put("/order", s.handleTapOrder)
			r.Patch("/links", s.handleTapLinks)
			r.Get("/{id}", s.handleGetTap)
			r.Put("/{id}", s.handleReplaceTap)
			r.Delete("/{id}", s.handleDeleteTap)
		})

		r.Route("/config", func(r chi.Router) {
			r.Get("/", s.handleGetConfig)
			r.Patch("/", s.handleUpdateConfig)
		})

		r.Route("/system", func(r chi.Router) {
			r.Get("/env", s.handleSystemEnv)
			r.Get("/logs", s.handleSystemLogs)
			r.Put("/log-level", s.handleSetLogLevel)
		})
	})

	r.Get("/theme.css", s.handleThemeCSS)

	r.Get("/ws", s.hub.ServeHTTP)

	r.Get("/", s.handleRoot)
	r.Get("/*", s.handleStatic)

	return r
}

// handleStatic serves the embedded UI.
//
// net/http's own file helpers are deliberately not used: they redirect
// "/index.html" to "/", which handleRoot redirects straight back to whichever
// page is set as home, so the two would loop.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

	file, err := s.static.Open(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "that page does not exist")
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "not_found", "that page does not exist")
		return
	}

	content, ok := file.(io.ReadSeeker)
	if !ok {
		// Every file in an embedded FS is seekable; this guards against a
		// future change of file system.
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		if _, err := io.Copy(w, file); err != nil {
			slog.Debug("failed to write a static file", "name", name, "error", err)
		}
		return
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
}

// handleRoot sends the browser to whichever page the user chose as home.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		slog.Error("failed to read the app configuration", "error", err)
		cfg = store.DefaultAppConfig()
	}
	target := "/taplist.html"
	if cfg.HomePage == store.HomePageKegs {
		target = "/kegs.html"
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleAlive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// errorResponse is the shape every failure uses.
type errorResponse struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line has already gone out, so this can only be logged.
		slog.Error("failed to write a JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, errorResponse{Error: code, Detail: detail})
}

// writeStoreError maps a store failure onto a response.
func writeStoreError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", what+" does not exist")
		return
	}
	slog.Error("database request failed", "what", what, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "the request could not be completed")
}

// writeCommandError maps a command failure onto a response.
func writeCommandError(w http.ResponseWriter, err error) {
	if errors.Is(err, keg.ErrNotConnected) {
		writeError(w, http.StatusServiceUnavailable, "not_connected", "the keg is not currently connected")
		return
	}
	writeError(w, http.StatusBadRequest, "command_failed", err.Error())
}

// decodeJSON reads a JSON request body, rejecting anything unparseable.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "the request body is not valid JSON")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Request value helpers
// ---------------------------------------------------------------------------

// numberOrString accepts a value that may be sent as a JSON number or as a
// string, which is what the browser sends from a text input.
type numberOrString struct {
	set   bool
	value float64
}

func (n *numberOrString) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		return nil
	}
	text = strings.Trim(text, `"`)
	if text == "" {
		return nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	n.set, n.value = true, f
	return nil
}

// Value returns the parsed number and whether one was supplied.
func (n numberOrString) Value() (float64, bool) { return n.value, n.set }

// Ptr returns the value as a pointer, or nil when unset.
func (n numberOrString) Ptr() *float64 {
	if !n.set {
		return nil
	}
	v := n.value
	return &v
}
