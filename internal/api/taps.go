package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

type tapRequest struct {
	TapNumber    *int           `json:"tap_number"`
	Name         string         `json:"name"`
	Brewery      string         `json:"brewery"`
	Style        string         `json:"style"`
	ABV          numberOrString `json:"abv"`
	IBU          numberOrString `json:"ibu"`
	SRM          numberOrString `json:"srm"`
	ColorPreset  string         `json:"color_preset"`
	Color        string         `json:"color"`
	Description  string         `json:"description"`
	TastingNotes string         `json:"tasting_notes"`
	KeggedDate   string         `json:"kegged_date"`
	KegID        string         `json:"keg_id"`
}

func (s *Server) handleListTaps(w http.ResponseWriter, r *http.Request) {
	taps, err := s.store.ListTaps()
	if err != nil {
		writeStoreError(w, err, "taps")
		return
	}
	writeJSON(w, http.StatusOK, taps)
}

func (s *Server) handleGetTap(w http.ResponseWriter, r *http.Request) {
	tap, err := s.store.GetTap(chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, http.StatusOK, tap)
}

// handleCreateTap adds a tap, answering 201 with its generated id.
func (s *Server) handleCreateTap(w http.ResponseWriter, r *http.Request) {
	s.saveTap(w, r, store.NewID(), http.StatusCreated)
}

// handleReplaceTap replaces every field of an existing tap.
func (s *Server) handleReplaceTap(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetTap(id); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	s.saveTap(w, r, id, http.StatusOK)
}

func (s *Server) saveTap(w http.ResponseWriter, r *http.Request, id string, status int) {
	var req tapRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	preset := strings.TrimSpace(req.ColorPreset)
	if !validBeerColor(w, req.SRM, preset) {
		return
	}
	keggedDate, err := store.NormalizeDate(req.KeggedDate, store.DateLayout)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_value", "kegged_date: "+err.Error())
		return
	}

	tap := &store.Tap{
		ID:           id,
		TapNumber:    req.TapNumber,
		Name:         strings.TrimSpace(req.Name),
		Brewery:      strings.TrimSpace(req.Brewery),
		Style:        strings.TrimSpace(req.Style),
		ABV:          req.ABV.Ptr(),
		IBU:          req.IBU.Ptr(),
		SRM:          req.SRM.Ptr(),
		ColorPreset:  preset,
		Color:        strings.TrimSpace(req.Color),
		Description:  strings.TrimSpace(req.Description),
		TastingNotes: strings.TrimSpace(req.TastingNotes),
		KeggedDate:   keggedDate,
		KegID:        strings.TrimSpace(req.KegID),
	}
	if err := s.store.SaveTap(tap); err != nil {
		var inUse *store.KegInUseError
		if errors.As(err, &inUse) {
			writeError(w, http.StatusConflict, "keg_in_use",
				"that keg scale is already linked to "+tapDescription(inUse.Tap)+"; unlink it there first")
			return
		}
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, status, map[string]any{"status": "ok", "id": tap.ID, "tap": tap})
}

// handleTapOrder saves the order the tap list was dragged into.
func (s *Server) handleTapOrder(w http.ResponseWriter, r *http.Request) {
	var req orderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.OrderedIDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "ordered_ids must be a non-empty array")
		return
	}
	if err := s.store.OrderTaps(req.OrderedIDs); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ordered_ids": req.OrderedIDs})
}

// handleTapLinks saves a drop on the Tap Setup board: the taps whose kegs
// changed, applied together so two taps can swap kegs. It returns every tap,
// so the page redraws the board as stored.
func (s *Server) handleTapLinks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Links []store.TapLink `json:"links"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Links) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "links must be a non-empty array")
		return
	}
	for i := range req.Links {
		req.Links[i].KegID = strings.TrimSpace(req.Links[i].KegID)
	}
	if err := s.store.LinkTaps(req.Links); err != nil {
		var inUse *store.KegInUseError
		if errors.As(err, &inUse) {
			writeError(w, http.StatusConflict, "keg_in_use",
				"that keg scale is already linked to "+tapDescription(inUse.Tap))
			return
		}
		writeStoreError(w, err, "tap")
		return
	}
	taps, err := s.store.ListTaps()
	if err != nil {
		writeStoreError(w, err, "taps")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "taps": taps})
}

// validBeerColor checks a colour given as an SRM or a named preset, writing
// the error itself. A drink is one or the other, never both.
func validBeerColor(w http.ResponseWriter, srm numberOrString, preset string) bool {
	v := srm.Ptr()
	switch {
	case v != nil && *v < 0:
		writeError(w, http.StatusBadRequest, "invalid_value", "srm cannot be negative")
	case preset != "" && !store.IsColorPreset(preset):
		writeError(w, http.StatusBadRequest, "invalid_value",
			"color_preset must be one of "+strings.Join(store.ColorPresets, ", "))
	case v != nil && preset != "":
		writeError(w, http.StatusBadRequest, "invalid_value", "set srm or color_preset, not both")
	default:
		return true
	}
	return false
}

func (s *Server) handleDeleteTap(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetTap(id); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	if err := s.store.DeleteTap(id); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "deleted": id})
}

// tapDescription names a tap the way the UI does: "Tap 3 (Red Barn Amber)".
func tapDescription(t *store.Tap) string {
	desc := "another tap"
	if t.TapNumber != nil {
		desc = fmt.Sprintf("Tap %d", *t.TapNumber)
	}
	if t.Name != "" {
		desc += " (" + t.Name + ")"
	}
	return desc
}

// maxTapImageBytes caps an uploaded tap picture. Tap Setup shrinks a picture
// well below this before sending it; the cap is for other clients.
const maxTapImageBytes = 4 << 20

// tapImageTypes are the picture formats every browser can show.
var tapImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// handleGetTapImage serves a tap's picture. Its URL carries a version that
// changes with the picture, so the browser may keep it indefinitely.
func (s *Server) handleGetTapImage(w http.ResponseWriter, r *http.Request) {
	img, err := s.store.GetTapImage(chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err, "tap image")
		return
	}
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(img.Data)
}

// handlePutTapImage stores the request body as a tap's picture, replacing any
// it had, and answers with the tap and its new image URL. The format is read
// from the bytes, not the request's Content-Type.
func (s *Server) handlePutTapImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetTap(id); err != nil {
		writeStoreError(w, err, "tap")
		return
	}

	defer r.Body.Close()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTapImageBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("the image must be at most %d MB", maxTapImageBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_body", "the image could not be read")
		return
	}
	contentType := http.DetectContentType(data)
	if !tapImageTypes[contentType] {
		writeError(w, http.StatusBadRequest, "invalid_value", "the image must be a JPEG, PNG, WebP or GIF")
		return
	}

	if err := s.store.SetTapImage(id, contentType, data); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	tap, err := s.store.GetTap(id)
	if err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "tap": tap})
}

// handleDeleteTapImage removes a tap's picture. A tap with none is not an
// error, so a repeated delete succeeds.
func (s *Server) handleDeleteTapImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetTap(id); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	if err := s.store.DeleteTapImage(id); err != nil {
		writeStoreError(w, err, "tap image")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
