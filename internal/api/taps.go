package api

import (
	"math"
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
	DeviceID     string         `json:"device_id"`
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

func (s *Server) handleSaveTap(w http.ResponseWriter, r *http.Request) {
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

	id := chi.URLParam(r, "id")
	// The UI posts "new" for a tap that does not exist yet.
	if id == "" || id == "new" {
		id = store.NewID()
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
		DeviceID:     strings.TrimSpace(req.DeviceID),
	}
	if err := s.store.SaveTap(tap); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": tap.ID, "tap": tap})
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

// displayTap is what an open-tap ESP32 display fetches. The field names are
// fixed by that firmware.
type displayTap struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// LogoURL is always empty: tap handle images are no longer kept, but the
	// firmware expects the field.
	LogoURL        string   `json:"logo_url"`
	KegCapacity    *float64 `json:"keg_capacity"`
	EmptyKegWeight *float64 `json:"empty_keg_weight"`
	CurrentWeight  *float64 `json:"current_weight"`
	KegID          string   `json:"keg_id"`
}

// litresPerUnit converts a remaining-beer reading to kilograms, which is what
// the display's own arithmetic expects. Beer is close enough to water that one
// litre is treated as one kilogram.
var weightPerUnit = map[string]float64{
	"litre": 1,
	"kg":    1,
	"lbs":   0.453592,
	"gal":   3.78541,
}

func (s *Server) handleGetKegForDisplay(w http.ResponseWriter, r *http.Request) {
	tap, err := s.store.GetTapByDeviceID(chi.URLParam(r, "deviceID"))
	if err != nil {
		writeStoreError(w, err, "tap for this display")
		return
	}

	out := displayTap{
		ID:          tap.ID,
		Name:        joinNonEmpty(" - ", tap.Name, tap.Brewery),
		Description: joinNonEmpty(" | ", tap.Description, tap.TastingNotes),
		KegID:       tap.KegID,
	}

	if tap.KegID != "" {
		if k, err := s.store.GetKeg(tap.KegID); err == nil {
			out.KegCapacity = k.MaxKegVolume
			out.EmptyKegWeight = k.EmptyKegWeight
			out.CurrentWeight = currentWeight(k)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// currentWeight estimates the total weight sitting on the scale.
//
// The raw scale reading is preferred when it looks sane; otherwise the
// remaining volume is converted and added to the empty keg's weight.
func currentWeight(k *store.Keg) *float64 {
	empty := 0.0
	if k.EmptyKegWeight != nil {
		empty = *k.EmptyKegWeight
	}

	// A raw reading outside this range means the scale is uncalibrated or
	// reporting in its own internal units.
	if k.WeightRaw != nil && *k.WeightRaw > 0 && *k.WeightRaw < 200 {
		total := round2(empty + *k.WeightRaw)
		return &total
	}
	if k.AmountLeft == nil {
		return nil
	}

	factor, ok := weightPerUnit[k.DeriveBeerLeftUnit()]
	if !ok {
		factor = 1
	}
	total := round2(empty + *k.AmountLeft*factor)
	return &total
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
