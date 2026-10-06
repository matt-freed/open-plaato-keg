package api

import (
	"encoding/json"
	"log/slog"
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
	units := s.displayUnits()
	store.SetDisplayAll(kegs, units)
	s.setLatestPours(kegs, units)
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
	s.writeKeg(w, k)
}

// writeKeg sends one keg the way the browser reads it: with its connection
// state, its display block and its latest pour.
func (s *Server) writeKeg(w http.ResponseWriter, k *store.Keg) {
	k.Connected = s.commander.Connected(k.ID)
	units := s.displayUnits()
	k.SetDisplay(units)
	s.setLatestPours([]*store.Keg{k}, units)
	writeJSON(w, http.StatusOK, k)
}

// handleUpdateKeg changes the settings kept here rather than on the device,
// which work whether or not the keg is connected. Each key present is
// changed and the rest are kept: label is text, and co2_capacity a number,
// or null to clear it. The response is the keg as it now stands.
func (s *Server) handleUpdateKeg(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeJSON(w, r, &fields) {
		return
	}
	if len(fields) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "name at least one setting to change")
		return
	}

	var label *string
	var co2Capacity *numberOrString
	for name, raw := range fields {
		var err error
		switch name {
		case "label":
			label = new(string)
			err = json.Unmarshal(raw, label)
		case "co2_capacity":
			co2Capacity = new(numberOrString)
			err = json.Unmarshal(raw, co2Capacity)
		default:
			writeError(w, http.StatusBadRequest, "invalid_value", name+" cannot be changed")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_value", name+" has an invalid value")
			return
		}
	}

	k, ok := s.updateKeg(w, chi.URLParam(r, "id"), func(k *store.Keg) {
		if label != nil {
			k.Label = strings.TrimSpace(*label)
		}
		if co2Capacity != nil {
			k.CO2Capacity = co2Capacity.Ptr()
		}
	})
	if !ok {
		return
	}
	s.writeKeg(w, k)
}

// connectionResponse describes a keg's live connection. Times are Unix
// seconds; everything but connected is omitted while the keg is offline.
type connectionResponse struct {
	Connected   bool   `json:"connected"`
	RemoteIP    string `json:"remote_ip,omitempty"`
	ConnectedAt int64  `json:"connected_at,omitempty"`
	LastHeard   int64  `json:"last_heard,omitempty"`
}

// handleKegConnection reports where a keg is connected from, since when, and
// when it last sent anything, heartbeats included.
func (s *Server) handleKegConnection(w http.ResponseWriter, r *http.Request) {
	k, err := s.store.GetKeg(chi.URLParam(r, "id"))
	if err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	info, ok := s.commander.Connection(k.ID)
	if !ok {
		writeJSON(w, http.StatusOK, connectionResponse{})
		return
	}
	writeJSON(w, http.StatusOK, connectionResponse{
		Connected:   true,
		RemoteIP:    info.RemoteIP,
		ConnectedAt: info.ConnectedAt.Unix(),
		LastHeard:   info.LastHeard.Unix(),
	})
}

// setLatestPours adds each keg's newest pour. A failure must not fail the
// request: the keg is still worth showing, just without its last pour.
func (s *Server) setLatestPours(kegs []*store.Keg, units store.DisplayUnits) {
	if err := s.store.SetLatestPours(kegs, units); err != nil {
		slog.Error("failed to read the latest pours", "error", err)
	}
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

	// Every id is checked first, so an unknown one changes nothing rather
	// than creating a phantom keg.
	for _, id := range req.OrderedIDs {
		if _, err := s.store.GetKeg(id); err != nil {
			writeStoreError(w, err, "keg")
			return
		}
	}
	for position, id := range req.OrderedIDs {
		if _, err := s.store.UpdateExistingKeg(id, func(k *store.Keg) { k.SortOrder = position }); err != nil {
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

// stringCommandRequest is the body for commands taking a named value.
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
//
// store.UpdateKeg creates a keg it does not find, which is right for device
// traffic but would let a request for an unknown or just-deleted id create a
// phantom keg, so the keg must already exist.
func (s *Server) updateKeg(w http.ResponseWriter, id string, mutate func(*store.Keg)) (*store.Keg, bool) {
	k, err := s.store.UpdateExistingKeg(id, mutate)
	if err != nil {
		writeStoreError(w, err, "keg")
		return nil, false
	}
	s.bus.Publish(events.Event{Kind: events.KegUpdated, KegID: id})
	return k, true
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
