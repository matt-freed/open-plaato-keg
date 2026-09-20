package api

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// TapHandleSize is the exact pixel size a tap handle image must be. The tap
// list lays handles out on a fixed grid, so anything else distorts.
const TapHandleSize = 200

// safeFilename bounds what an uploaded name may contain, so a request can
// never reach outside the uploads directory.
var safeFilename = regexp.MustCompile(`^[a-zA-Z0-9_-]+\.jpg$`)

func (s *Server) handleListTapHandles(w http.ResponseWriter, r *http.Request) {
	handles, err := s.store.ListTapHandles()
	if err != nil {
		writeStoreError(w, err, "tap handles")
		return
	}
	writeJSON(w, http.StatusOK, handles)
}

func (s *Server) handleUploadTapHandle(w http.ResponseWriter, r *http.Request) {
	file, header, err := s.openUpload(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", err.Error())
		return
	}
	defer file.Close()

	if !strings.EqualFold(filepath.Ext(header.Filename), ".jpg") &&
		!strings.EqualFold(filepath.Ext(header.Filename), ".jpeg") {
		writeError(w, http.StatusBadRequest, "invalid_type", "the image must be a JPEG")
		return
	}

	cfg, format, err := image.DecodeConfig(file)
	if err != nil || format != "jpeg" {
		writeError(w, http.StatusBadRequest, "invalid_type", "the image could not be read as a JPEG")
		return
	}
	if cfg.Width != TapHandleSize || cfg.Height != TapHandleSize {
		writeError(w, http.StatusBadRequest, "invalid_dimensions",
			fmt.Sprintf("the image must be exactly %dx%d pixels, got %dx%d",
				TapHandleSize, TapHandleSize, cfg.Width, cfg.Height))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "the upload could not be read")
		return
	}

	// The stored name is generated rather than taken from the upload, so it
	// cannot collide with an existing handle or escape the directory.
	filename := store.NewID() + ".jpg"
	if err := os.MkdirAll(s.cfg.TapHandleDir(), 0o755); err != nil {
		slog.Error("failed to create the tap handle directory", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "the image could not be saved")
		return
	}
	if err := writeFile(filepath.Join(s.cfg.TapHandleDir(), filename), file); err != nil {
		slog.Error("failed to save a tap handle", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "the image could not be saved")
		return
	}

	if err := s.store.AddTapHandle(filename, time.Now()); err != nil {
		writeStoreError(w, err, "tap handle")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "filename": filename})
}

func (s *Server) handleServeTapHandle(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	if !safeFilename.MatchString(filename) {
		writeError(w, http.StatusBadRequest, "invalid_filename", "that is not a valid handle name")
		return
	}

	path := filepath.Join(s.cfg.TapHandleDir(), filename)
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "that handle does not exist")
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "that handle does not exist")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (s *Server) handleDeleteTapHandle(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	if !safeFilename.MatchString(filename) {
		writeError(w, http.StatusBadRequest, "invalid_filename", "that is not a valid handle name")
		return
	}

	if err := s.store.DeleteTapHandle(filename); err != nil {
		writeStoreError(w, err, "tap handle")
		return
	}
	// The record is what the UI lists, so a leftover file is harmless but a
	// leftover record is not.
	if err := os.Remove(filepath.Join(s.cfg.TapHandleDir(), filename)); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to remove a tap handle file", "filename", filename, "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "deleted": filename})
}

// backgroundTypes are the image formats accepted for the page background,
// mapped to the extension they are stored under.
var backgroundTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

func (s *Server) backgroundPath() (string, bool) {
	for _, ext := range backgroundTypes {
		path := filepath.Join(s.cfg.DataDir(), "background"+ext)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func (s *Server) handleUploadBackground(w http.ResponseWriter, r *http.Request) {
	file, header, err := s.openUpload(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", err.Error())
		return
	}
	defer file.Close()

	ext, ok := backgroundTypes[header.Header.Get("Content-Type")]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_type", "the background must be a JPEG, PNG, WebP or GIF")
		return
	}

	// Only one background exists at a time, so any previous one goes first —
	// otherwise a JPEG would still be served after a PNG replaced it.
	s.removeBackground()

	if err := os.MkdirAll(s.cfg.DataDir(), 0o755); err != nil {
		slog.Error("failed to create the data directory", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "the image could not be saved")
		return
	}
	if err := writeFile(filepath.Join(s.cfg.DataDir(), "background"+ext), file); err != nil {
		slog.Error("failed to save the background", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "the image could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "url": "/uploads/background"})
}

func (s *Server) handleServeBackground(w http.ResponseWriter, r *http.Request) {
	path, ok := s.backgroundPath()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no background has been uploaded")
		return
	}

	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no background has been uploaded")
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no background has been uploaded")
		return
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func (s *Server) handleDeleteBackground(w http.ResponseWriter, r *http.Request) {
	s.removeBackground()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) removeBackground() {
	for _, ext := range backgroundTypes {
		path := filepath.Join(s.cfg.DataDir(), "background"+ext)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove a background image", "path", path, "error", err)
		}
	}
}

// openUpload reads the "image" field from a multipart request.
func (s *Server) openUpload(w http.ResponseWriter, r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return nil, nil, fmt.Errorf("the upload could not be read: %w", err)
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		return nil, nil, fmt.Errorf("the request has no \"image\" field")
	}
	return file, header, nil
}

func writeFile(path string, src io.Reader) error {
	// Write to a temporary file first, so a failed upload cannot leave a
	// truncated image in place of a good one.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
