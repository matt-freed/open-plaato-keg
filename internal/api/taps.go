package api

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

type tapRequest struct {
	TapNumber      *int           `json:"tap_number"`
	Name           string         `json:"name"`
	Brewery        string         `json:"brewery"`
	Style          string         `json:"style"`
	ABV            numberOrString `json:"abv"`
	IBU            numberOrString `json:"ibu"`
	Color          string         `json:"color"`
	Description    string         `json:"description"`
	TastingNotes   string         `json:"tasting_notes"`
	ExpirationDate string         `json:"expiration_date"`
	KegID          string         `json:"keg_id"`
	HandleImage    string         `json:"handle_image"`
	DeviceID       string         `json:"device_id"`
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

	id := chi.URLParam(r, "id")
	// The UI posts "new" for a tap that does not exist yet.
	if id == "" || id == "new" {
		id = store.NewID()
	}

	tap := &store.Tap{
		ID:             id,
		TapNumber:      req.TapNumber,
		Name:           strings.TrimSpace(req.Name),
		Brewery:        strings.TrimSpace(req.Brewery),
		Style:          strings.TrimSpace(req.Style),
		ABV:            req.ABV.Ptr(),
		IBU:            req.IBU.Ptr(),
		Color:          strings.TrimSpace(req.Color),
		Description:    strings.TrimSpace(req.Description),
		TastingNotes:   strings.TrimSpace(req.TastingNotes),
		ExpirationDate: strings.TrimSpace(req.ExpirationDate),
		KegID:          strings.TrimSpace(req.KegID),
		HandleImage:    strings.TrimSpace(req.HandleImage),
		DeviceID:       strings.TrimSpace(req.DeviceID),
	}
	if err := s.store.SaveTap(tap); err != nil {
		writeStoreError(w, err, "tap")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": tap.ID, "tap": tap})
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

type beverageRequest struct {
	Name         string         `json:"name"`
	Brewery      string         `json:"brewery"`
	Style        string         `json:"style"`
	ABV          numberOrString `json:"abv"`
	IBU          numberOrString `json:"ibu"`
	Color        string         `json:"color"`
	Description  string         `json:"description"`
	TastingNotes string         `json:"tasting_notes"`
	OG           numberOrString `json:"og"`
	FG           numberOrString `json:"fg"`
	SRM          numberOrString `json:"srm"`
	Source       string         `json:"source"`
}

func (s *Server) handleListBeverages(w http.ResponseWriter, r *http.Request) {
	beverages, err := s.store.ListBeverages()
	if err != nil {
		writeStoreError(w, err, "beverages")
		return
	}
	writeJSON(w, http.StatusOK, beverages)
}

func (s *Server) handleGetBeverage(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.GetBeverage(chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err, "beverage")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleSaveBeverage(w http.ResponseWriter, r *http.Request) {
	var req beverageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	id := chi.URLParam(r, "id")
	createdAt := time.Now().Unix()
	if id == "" || id == "new" {
		id = store.NewID()
	} else if existing, err := s.store.GetBeverage(id); err == nil {
		createdAt = existing.CreatedAt
	}

	b := &store.Beverage{
		ID:           id,
		Name:         strings.TrimSpace(req.Name),
		Brewery:      strings.TrimSpace(req.Brewery),
		Style:        strings.TrimSpace(req.Style),
		ABV:          req.ABV.Ptr(),
		IBU:          req.IBU.Ptr(),
		Color:        strings.TrimSpace(req.Color),
		Description:  strings.TrimSpace(req.Description),
		TastingNotes: strings.TrimSpace(req.TastingNotes),
		OG:           req.OG.Ptr(),
		FG:           req.FG.Ptr(),
		SRM:          req.SRM.Ptr(),
		Source:       strings.TrimSpace(req.Source),
		CreatedAt:    createdAt,
	}

	// A recipe usually records its gravities but not its strength; deriving it
	// saves the user doing the arithmetic.
	if b.ABV == nil && b.OG != nil && b.FG != nil {
		abv := store.EstimateABV(*b.OG, *b.FG)
		b.ABV = &abv
	}

	if err := s.store.SaveBeverage(b); err != nil {
		writeStoreError(w, err, "beverage")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": b.ID, "beverage": b})
}

func (s *Server) handleDeleteBeverage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetBeverage(id); err != nil {
		writeStoreError(w, err, "beverage")
		return
	}
	if err := s.store.DeleteBeverage(id); err != nil {
		writeStoreError(w, err, "beverage")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "deleted": id})
}

// displayTap is what an open-tap ESP32 display fetches. The field names are
// fixed by that firmware.
type displayTap struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
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
	if tap.HandleImage != "" {
		out.LogoURL = absoluteURL(r, "/uploads/tap-handles/"+tap.HandleImage)
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

// absoluteURL builds a URL the display can fetch, using the host it reached us
// on.
func absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host + path
}
