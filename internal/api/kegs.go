package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/keg"
	"github.com/matt-freed/open-plaato-keg/internal/store"
)

func (s *Server) handleListKegs(w http.ResponseWriter, r *http.Request) {
	kegs, err := s.store.ListKegs()
	if err != nil {
		writeStoreError(w, err, "kegs")
		return
	}
	for _, k := range kegs {
		k.Connected = s.commander.Connected(k.ID)
	}
	store.SetDisplayAll(kegs, s.displayUnits())
	if kegs == nil {
		kegs = []*store.Keg{}
	}
	writeJSON(w, http.StatusOK, kegs)
}

func (s *Server) handleListKegIDs(w http.ResponseWriter, r *http.Request) {
	ids, err := s.store.ListKegIDs()
	if err != nil {
		writeStoreError(w, err, "kegs")
		return
	}
	writeJSON(w, http.StatusOK, ids)
}

func (s *Server) handleListConnected(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.commander.ConnectedIDs())
}

func (s *Server) handleGetKeg(w http.ResponseWriter, r *http.Request) {
	k, err := s.store.GetKeg(chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	k.Connected = s.commander.Connected(k.ID)
	k.SetDisplay(s.displayUnits())
	writeJSON(w, http.StatusOK, k)
}

// displayUnits reads the presentation preference.
//
// A failure here must not fail the request: the default follows the device, so
// the reading is shown exactly as the scale reports it.
func (s *Server) displayUnits() store.DisplayUnits {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		return store.DefaultAppConfig().DisplayUnits
	}
	return cfg.DisplayUnits
}

func (s *Server) handleDeleteKeg(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetKeg(id); err != nil {
		writeStoreError(w, err, "keg")
		return
	}

	// Closing the socket stops the keg re-creating itself with its next
	// packet; it will reconnect and register again if it is still powered on.
	s.commander.Disconnect(id)

	if err := s.store.DeleteKeg(id); err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	s.bus.Publish(events.Event{Kind: events.KegRemoved, KegID: id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "deleted": id})
}

type orderRequest struct {
	OrderedIDs []string `json:"ordered_ids"`
}

func (s *Server) handleKegOrder(w http.ResponseWriter, r *http.Request) {
	var req orderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.OrderedIDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "ordered_ids must be a non-empty array")
		return
	}

	for position, id := range req.OrderedIDs {
		if _, err := s.store.UpdateKeg(id, func(k *store.Keg) { k.SortOrder = position }); err != nil {
			writeStoreError(w, err, "keg")
			return
		}
		s.bus.Publish(events.Event{Kind: events.KegUpdated, KegID: id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ordered_ids": req.OrderedIDs})
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// commandRequest is the body every value-taking command accepts. The value may
// be sent as a JSON number or a string.
type commandRequest struct {
	Value numberOrString `json:"value"`
}

// stringCommandRequest is the body for commands taking free text.
type stringCommandRequest struct {
	Value string `json:"value"`
}

func (s *Server) mountKegCommands(r chi.Router) {
	// Momentary buttons: the device acts on the press and needs the release
	// before it will fire again, so the UI sends them as a pair.
	r.Post("/tare", s.kegAction("tare", func(id string) error { return s.commander.Tare(id) }))
	r.Post("/tare-release", s.kegAction("tare_release", func(id string) error { return s.commander.TareRelease(id) }))
	r.Post("/empty-keg", s.kegAction("empty_keg", func(id string) error { return s.commander.SetEmptyKeg(id) }))
	r.Post("/empty-keg-release", s.kegAction("empty_keg_release", func(id string) error { return s.commander.SetEmptyKegRelease(id) }))

	r.Post("/empty-keg-weight", s.kegValueCommand("empty_keg_weight", func(id string, v float64) error {
		return s.commander.SetEmptyKegWeight(id, v)
	}))
	r.Post("/max-keg-volume", s.kegValueCommand("max_keg_volume", func(id string, v float64) error {
		return s.commander.SetMaxKegVolume(id, v)
	}))
	r.Post("/temperature-offset", s.kegValueCommand("temperature_offset", func(id string, v float64) error {
		return s.commander.SetTemperatureOffset(id, v)
	}))
	r.Post("/calibrate-known-weight", s.kegValueCommand("calibrate_known_weight", func(id string, v float64) error {
		return s.commander.CalibrateKnownWeight(id, v)
	}))

	r.Post("/unit", s.kegEnumCommand("unit", map[string]int{
		"metric": keg.UnitMetric, "1": keg.UnitMetric,
		"us": keg.UnitUS, "2": keg.UnitUS,
	}, func(id string, v int) error { return s.commander.SetUnit(id, v) }))
	r.Post("/measure-unit", s.kegEnumCommand("measure_unit", map[string]int{
		"weight": keg.MeasureWeight, "1": keg.MeasureWeight,
		"volume": keg.MeasureVolume, "2": keg.MeasureVolume,
	}, func(id string, v int) error { return s.commander.SetMeasureUnit(id, v) }))
	r.Post("/keg-mode", s.kegEnumCommand("keg_mode", map[string]int{
		"beer": keg.ModeBeer, "1": keg.ModeBeer,
		"co2": keg.ModeCO2, "2": keg.ModeCO2,
	}, func(id string, v int) error { return s.commander.SetKegMode(id, v) }))
	r.Post("/sensitivity", s.kegEnumCommand("sensitivity", map[string]int{
		"very_low": 1, "1": 1,
		"low": 2, "2": 2,
		"medium": 3, "3": 3,
		"high": 4, "4": 4,
	}, func(id string, v int) error { return s.commander.SetSensitivity(id, v) }))

	// Settings the device shows but never reports back, so they are also kept
	// here to survive a restart.
	r.Post("/beer-style", s.handleSetBeerStyle)
	r.Post("/date", s.handleSetKegDate)

	// Settings the device has no pin for at all.
	r.Post("/label", s.handleSetLabel)
	r.Post("/display-mode", s.handleSetDisplayMode)
	r.Post("/og", s.handleSetOG)
	r.Post("/fg", s.handleSetFG)
	r.Post("/abv", s.handleSetABV)
	r.Post("/co2-capacity", s.handleSetCO2Capacity)
	r.Post("/reset-last-pour", s.handleResetLastPour)
}

// kegAction builds a handler for a command that takes no value.
func (s *Server) kegAction(name string, run func(id string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := run(id); err != nil {
			writeCommandError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": name})
	}
}

// kegValueCommand builds a handler for a command taking a numeric value.
func (s *Server) kegValueCommand(name string, run func(id string, value float64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req commandRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		value, ok := req.Value.Value()
		if !ok {
			writeError(w, http.StatusBadRequest, "missing_value", "value is required and must be a number")
			return
		}
		if err := run(chi.URLParam(r, "id"), value); err != nil {
			writeCommandError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": name, "value": value})
	}
}

// kegEnumCommand builds a handler for a command taking one of a fixed set of
// values, accepted either by name or by the device's own numeric code.
func (s *Server) kegEnumCommand(name string, allowed map[string]int, run func(id string, value int) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req stringCommandRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		value, ok := allowed[strings.ToLower(strings.TrimSpace(req.Value))]
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_value",
				"value must be one of "+strings.Join(sortedKeys(allowed), ", "))
			return
		}
		if err := run(chi.URLParam(r, "id"), value); err != nil {
			writeCommandError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": name, "value": value})
	}
}

// updateKeg applies a change to the stored keg and broadcasts it.
func (s *Server) updateKeg(w http.ResponseWriter, id string, mutate func(*store.Keg)) (*store.Keg, bool) {
	k, err := s.store.UpdateKeg(id, mutate)
	if err != nil {
		writeStoreError(w, err, "keg")
		return nil, false
	}
	s.bus.Publish(events.Event{Kind: events.KegUpdated, KegID: id})
	return k, true
}

func (s *Server) handleSetLabel(w http.ResponseWriter, r *http.Request) {
	var req stringCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	label := strings.TrimSpace(req.Value)
	if _, ok := s.updateKeg(w, chi.URLParam(r, "id"), func(k *store.Keg) { k.Label = label }); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": "label", "value": label})
}

func (s *Server) handleSetDisplayMode(w http.ResponseWriter, r *http.Request) {
	var req stringCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	mode := strings.TrimSpace(req.Value)
	if mode != store.DisplayWeightPrimary && mode != store.DisplayPercentPrimary {
		writeError(w, http.StatusBadRequest, "invalid_value",
			"display mode must be "+store.DisplayWeightPrimary+" or "+store.DisplayPercentPrimary)
		return
	}
	if _, ok := s.updateKeg(w, chi.URLParam(r, "id"), func(k *store.Keg) { k.DisplayMode = mode }); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": "display_mode", "value": mode})
}

// handleSetBeerStyle writes the style to the device's display and keeps a copy,
// because the device never reports this pin back.
func (s *Server) handleSetBeerStyle(w http.ResponseWriter, r *http.Request) {
	var req stringCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "id")
	style := strings.TrimSpace(req.Value)

	if _, ok := s.updateKeg(w, id, func(k *store.Keg) { k.BeerStyle = style }); !ok {
		return
	}
	// The device may be offline; the stored value is still worth keeping.
	sent := s.commander.SetBeerStyle(id, style) == nil
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "command": "beer_style", "value": style, "sent_to_device": sent,
	})
}

func (s *Server) handleSetKegDate(w http.ResponseWriter, r *http.Request) {
	var req stringCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "id")
	date := strings.TrimSpace(req.Value)

	if _, ok := s.updateKeg(w, id, func(k *store.Keg) { k.KegDate = date }); !ok {
		return
	}
	sent := s.commander.SetDate(id, date) == nil
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "command": "date", "value": date, "sent_to_device": sent,
	})
}

func (s *Server) handleSetOG(w http.ResponseWriter, r *http.Request) {
	s.storeNumber(w, r, "og", func(k *store.Keg, v *float64) { k.OG = v })
}

func (s *Server) handleSetFG(w http.ResponseWriter, r *http.Request) {
	s.storeNumber(w, r, "fg", func(k *store.Keg, v *float64) { k.FG = v })
}

func (s *Server) handleSetCO2Capacity(w http.ResponseWriter, r *http.Request) {
	s.storeNumber(w, r, "co2_capacity", func(k *store.Keg, v *float64) { k.CO2Capacity = v })
}

func (s *Server) storeNumber(w http.ResponseWriter, r *http.Request, name string, set func(*store.Keg, *float64)) {
	var req commandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	value, ok := req.Value.Value()
	if !ok {
		writeError(w, http.StatusBadRequest, "missing_value", "value is required and must be a number")
		return
	}
	if _, ok := s.updateKeg(w, chi.URLParam(r, "id"), func(k *store.Keg) { set(k, req.Value.Ptr()) }); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": name, "value": value})
}

type abvRequest struct {
	OG numberOrString `json:"og"`
	FG numberOrString `json:"fg"`
}

// handleSetABV computes and stores alcohol by volume from the two gravities.
func (s *Server) handleSetABV(w http.ResponseWriter, r *http.Request) {
	var req abvRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	og, okOG := req.OG.Value()
	fg, okFG := req.FG.Value()
	if !okOG || !okFG {
		writeError(w, http.StatusBadRequest, "missing_value", "og and fg are both required and must be numbers")
		return
	}

	abv := store.EstimateABV(og, fg)
	_, ok := s.updateKeg(w, chi.URLParam(r, "id"), func(k *store.Keg) {
		k.OG, k.FG, k.ABV = req.OG.Ptr(), req.FG.Ptr(), &abv
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": "abv", "value": abv})
}

// handleResetLastPour clears the last pour reading, which is otherwise only
// replaced by the next pour.
func (s *Server) handleResetLastPour(w http.ResponseWriter, r *http.Request) {
	zero := 0.0
	if _, ok := s.updateKeg(w, chi.URLParam(r, "id"), func(k *store.Keg) { k.LastPour = &zero }); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "command": "reset_last_pour"})
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// A stable list keeps the error message deterministic.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
