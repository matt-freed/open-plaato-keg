package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// Display modes for the keg tile in the web UI.
const (
	DisplayWeightPrimary  = "weight_primary"
	DisplayPercentPrimary = "percent_primary"
)

// Keg is the full state of one keg.
//
// Device-reported fields are pointers because "the device has never sent this
// pin" and "the device reported zero" mean different things: an uncalibrated
// scale legitimately reports 0, and a keg that has not reported its
// temperature should not display 0 °C.
type Keg struct {
	ID string `json:"id"`

	AmountLeft              *float64 `json:"amount_left"`
	PercentOfBeerLeft       *float64 `json:"percent_of_beer_left"`
	IsPouring               *bool    `json:"is_pouring"`
	KegTemperature          *float64 `json:"keg_temperature"`
	LastPour                *float64 `json:"last_pour"`
	LastPourString          *string  `json:"last_pour_string"`
	TemperatureOffset       *float64 `json:"temperature_offset"`
	TemperatureCorrection   *float64 `json:"temperature_correction"`
	WeightRaw               *float64 `json:"weight_raw"`
	VolumeRaw               *float64 `json:"volume_raw"`
	PourVolumeRaw           *float64 `json:"pour_volume_raw"`
	EmptyKegWeight          *float64 `json:"empty_keg_weight"`
	MaxKegVolume            *float64 `json:"max_keg_volume"`
	MinTemperature          *float64 `json:"min_temperature"`
	MaxTemperature          *float64 `json:"max_temperature"`
	MinTemperatureMax       *float64 `json:"min_temperature_max"`
	MaxTemperatureMin       *float64 `json:"max_temperature_min"`
	Unit                    *int64   `json:"unit"`
	MeasureUnit             *int64   `json:"measure_unit"`
	KegMode                 *int64   `json:"keg_mode"`
	Sensitivity             *int64   `json:"sensitivity"`
	WeightUnit              *string  `json:"weight_unit"`
	BeerLeftUnitDevice      *string  `json:"-"`
	VolumeUnit              *string  `json:"volume_unit"`
	TemperatureUnit         *string  `json:"temperature_unit"`
	KegTemperatureString    *string  `json:"keg_temperature_string"`
	ChipTemperatureString   *string  `json:"chip_temperature_string"`
	CalculatedABV           *float64 `json:"calculated_abv"`
	CalculatedAlcoholString *string  `json:"calculated_alcohol_string"`
	WifiSignalStrength      *int64   `json:"wifi_signal_strength"`
	LeakDetection           *int64   `json:"leak_detection"`
	FirmwareVersion         *string  `json:"firmware_version"`
	DeviceOG                *float64 `json:"device_og"`
	DeviceFG                *float64 `json:"device_fg"`
	DeviceBeerStyle         *string  `json:"device_beer_style"`
	DeviceDate              *string  `json:"device_date"`

	// User-set fields, held only here.
	Label       string   `json:"label"`
	DisplayMode string   `json:"display_mode"`
	SortOrder   int      `json:"sort_order"`
	BeerStyle   string   `json:"beer_style"`
	KegDate     string   `json:"keg_date"`
	OG          *float64 `json:"og"`
	FG          *float64 `json:"fg"`
	ABV         *float64 `json:"abv"`
	CO2Capacity *float64 `json:"co2_capacity"`

	Internal map[string]string `json:"internal"`
	Extra    map[string]string `json:"extra,omitempty"`

	FirstSeen int64 `json:"first_seen"`
	LastSeen  int64 `json:"last_seen"`

	// BeerLeftUnit is always derived from unit, measure_unit and keg_mode
	// rather than trusted from the device, so the displayed label cannot
	// disagree with the configured mode.
	BeerLeftUnit string `json:"beer_left_unit"`
	// Connected is filled in by the API from the live connection registry.
	Connected bool `json:"connected"`
}

// kegColumn ties a database column to its Go field, so the column list, the
// insert values and the scan destinations cannot drift apart.
type kegColumn struct {
	name string
	val  func(*Keg) any
	dest func(*Keg) any
}

var kegColumns = []kegColumn{
	{"id", func(k *Keg) any { return k.ID }, func(k *Keg) any { return &k.ID }},
	{"amount_left", func(k *Keg) any { return k.AmountLeft }, func(k *Keg) any { return &k.AmountLeft }},
	{"percent_of_beer_left", func(k *Keg) any { return k.PercentOfBeerLeft }, func(k *Keg) any { return &k.PercentOfBeerLeft }},
	{"is_pouring", func(k *Keg) any { return k.IsPouring }, func(k *Keg) any { return &k.IsPouring }},
	{"keg_temperature", func(k *Keg) any { return k.KegTemperature }, func(k *Keg) any { return &k.KegTemperature }},
	{"last_pour", func(k *Keg) any { return k.LastPour }, func(k *Keg) any { return &k.LastPour }},
	{"last_pour_string", func(k *Keg) any { return k.LastPourString }, func(k *Keg) any { return &k.LastPourString }},
	{"temperature_offset", func(k *Keg) any { return k.TemperatureOffset }, func(k *Keg) any { return &k.TemperatureOffset }},
	{"temperature_correction", func(k *Keg) any { return k.TemperatureCorrection }, func(k *Keg) any { return &k.TemperatureCorrection }},
	{"weight_raw", func(k *Keg) any { return k.WeightRaw }, func(k *Keg) any { return &k.WeightRaw }},
	{"volume_raw", func(k *Keg) any { return k.VolumeRaw }, func(k *Keg) any { return &k.VolumeRaw }},
	{"pour_volume_raw", func(k *Keg) any { return k.PourVolumeRaw }, func(k *Keg) any { return &k.PourVolumeRaw }},
	{"empty_keg_weight", func(k *Keg) any { return k.EmptyKegWeight }, func(k *Keg) any { return &k.EmptyKegWeight }},
	{"max_keg_volume", func(k *Keg) any { return k.MaxKegVolume }, func(k *Keg) any { return &k.MaxKegVolume }},
	{"min_temperature", func(k *Keg) any { return k.MinTemperature }, func(k *Keg) any { return &k.MinTemperature }},
	{"max_temperature", func(k *Keg) any { return k.MaxTemperature }, func(k *Keg) any { return &k.MaxTemperature }},
	{"min_temperature_max", func(k *Keg) any { return k.MinTemperatureMax }, func(k *Keg) any { return &k.MinTemperatureMax }},
	{"max_temperature_min", func(k *Keg) any { return k.MaxTemperatureMin }, func(k *Keg) any { return &k.MaxTemperatureMin }},
	{"unit", func(k *Keg) any { return k.Unit }, func(k *Keg) any { return &k.Unit }},
	{"measure_unit", func(k *Keg) any { return k.MeasureUnit }, func(k *Keg) any { return &k.MeasureUnit }},
	{"keg_mode", func(k *Keg) any { return k.KegMode }, func(k *Keg) any { return &k.KegMode }},
	{"sensitivity", func(k *Keg) any { return k.Sensitivity }, func(k *Keg) any { return &k.Sensitivity }},
	{"weight_unit", func(k *Keg) any { return k.WeightUnit }, func(k *Keg) any { return &k.WeightUnit }},
	{"beer_left_unit_device", func(k *Keg) any { return k.BeerLeftUnitDevice }, func(k *Keg) any { return &k.BeerLeftUnitDevice }},
	{"volume_unit", func(k *Keg) any { return k.VolumeUnit }, func(k *Keg) any { return &k.VolumeUnit }},
	{"temperature_unit", func(k *Keg) any { return k.TemperatureUnit }, func(k *Keg) any { return &k.TemperatureUnit }},
	{"keg_temperature_string", func(k *Keg) any { return k.KegTemperatureString }, func(k *Keg) any { return &k.KegTemperatureString }},
	{"chip_temperature_string", func(k *Keg) any { return k.ChipTemperatureString }, func(k *Keg) any { return &k.ChipTemperatureString }},
	{"calculated_abv", func(k *Keg) any { return k.CalculatedABV }, func(k *Keg) any { return &k.CalculatedABV }},
	{"calculated_alcohol_string", func(k *Keg) any { return k.CalculatedAlcoholString }, func(k *Keg) any { return &k.CalculatedAlcoholString }},
	{"wifi_signal_strength", func(k *Keg) any { return k.WifiSignalStrength }, func(k *Keg) any { return &k.WifiSignalStrength }},
	{"leak_detection", func(k *Keg) any { return k.LeakDetection }, func(k *Keg) any { return &k.LeakDetection }},
	{"firmware_version", func(k *Keg) any { return k.FirmwareVersion }, func(k *Keg) any { return &k.FirmwareVersion }},
	{"device_og", func(k *Keg) any { return k.DeviceOG }, func(k *Keg) any { return &k.DeviceOG }},
	{"device_fg", func(k *Keg) any { return k.DeviceFG }, func(k *Keg) any { return &k.DeviceFG }},
	{"device_beer_style", func(k *Keg) any { return k.DeviceBeerStyle }, func(k *Keg) any { return &k.DeviceBeerStyle }},
	{"device_date", func(k *Keg) any { return k.DeviceDate }, func(k *Keg) any { return &k.DeviceDate }},
	{"label", func(k *Keg) any { return k.Label }, func(k *Keg) any { return &k.Label }},
	{"display_mode", func(k *Keg) any { return k.DisplayMode }, func(k *Keg) any { return &k.DisplayMode }},
	{"sort_order", func(k *Keg) any { return k.SortOrder }, func(k *Keg) any { return &k.SortOrder }},
	{"beer_style", func(k *Keg) any { return k.BeerStyle }, func(k *Keg) any { return &k.BeerStyle }},
	{"keg_date", func(k *Keg) any { return k.KegDate }, func(k *Keg) any { return &k.KegDate }},
	{"og", func(k *Keg) any { return k.OG }, func(k *Keg) any { return &k.OG }},
	{"fg", func(k *Keg) any { return k.FG }, func(k *Keg) any { return &k.FG }},
	{"abv", func(k *Keg) any { return k.ABV }, func(k *Keg) any { return &k.ABV }},
	{"co2_capacity", func(k *Keg) any { return k.CO2Capacity }, func(k *Keg) any { return &k.CO2Capacity }},
	{"internal", func(k *Keg) any { return encodeMap(k.Internal) }, nil},
	{"extra", func(k *Keg) any { return encodeMap(k.Extra) }, nil},
	{"first_seen", func(k *Keg) any { return k.FirstSeen }, func(k *Keg) any { return &k.FirstSeen }},
	{"last_seen", func(k *Keg) any { return k.LastSeen }, func(k *Keg) any { return &k.LastSeen }},
}

var (
	kegColumnList  string
	kegPlaceholder string
)

func init() {
	names := make([]string, len(kegColumns))
	holders := make([]string, len(kegColumns))
	for i, c := range kegColumns {
		names[i] = c.name
		holders[i] = "?"
	}
	kegColumnList = strings.Join(names, ", ")
	kegPlaceholder = strings.Join(holders, ", ")
}

func encodeMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeMap(s string) map[string]string {
	m := map[string]string{}
	if s == "" {
		return m
	}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// scanKeg reads one row in kegColumns order.
func scanKeg(row interface{ Scan(...any) error }) (*Keg, error) {
	k := &Keg{}
	var internalJSON, extraJSON string
	dests := make([]any, 0, len(kegColumns))
	for _, c := range kegColumns {
		switch c.name {
		case "internal":
			dests = append(dests, &internalJSON)
		case "extra":
			dests = append(dests, &extraJSON)
		default:
			dests = append(dests, c.dest(k))
		}
	}
	if err := row.Scan(dests...); err != nil {
		return nil, err
	}
	k.Internal = decodeMap(internalJSON)
	k.Extra = decodeMap(extraJSON)
	k.BeerLeftUnit = k.DeriveBeerLeftUnit()
	return k, nil
}

func (s *Store) writeKeg(tx *sql.Tx, k *Keg) error {
	values := make([]any, len(kegColumns))
	for i, c := range kegColumns {
		values[i] = c.val(k)
	}
	query := fmt.Sprintf("INSERT OR REPLACE INTO kegs (%s) VALUES (%s)", kegColumnList, kegPlaceholder)
	_, err := tx.Exec(query, values...)
	return err
}

// GetKeg returns one keg, or ErrNotFound.
func (s *Store) GetKeg(id string) (*Keg, error) {
	query := fmt.Sprintf("SELECT %s FROM kegs WHERE id = ?", kegColumnList)
	k, err := scanKeg(s.db.QueryRow(query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// ListKegs returns every keg, ordered for display.
func (s *Store) ListKegs() ([]*Keg, error) {
	query := fmt.Sprintf("SELECT %s FROM kegs ORDER BY sort_order, id", kegColumnList)
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var kegs []*Keg
	for rows.Next() {
		k, err := scanKeg(rows)
		if err != nil {
			return nil, err
		}
		kegs = append(kegs, k)
	}
	return kegs, rows.Err()
}

// ListKegIDs returns the id of every known keg.
func (s *Store) ListKegIDs() ([]string, error) {
	rows, err := s.db.Query("SELECT id FROM kegs ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteKeg removes a keg and its logged history.
func (s *Store) DeleteKeg(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM kegs WHERE id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM keg_log WHERE keg_id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateKeg applies mutate to the keg with the given id inside a transaction,
// creating the row if it does not exist yet.
//
// Read-modify-write reproduces the merge semantics the application depends on:
// a packet only carries the handful of pins that changed, and fields it does
// not mention must keep their stored value.
func (s *Store) UpdateKeg(id string, mutate func(*Keg)) (*Keg, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	query := fmt.Sprintf("SELECT %s FROM kegs WHERE id = ?", kegColumnList)
	k, err := scanKeg(tx.QueryRow(query, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		k = newKeg(id)
	case err != nil:
		return nil, err
	}

	mutate(k)

	k.LastSeen = time.Now().Unix()
	if k.FirstSeen == 0 {
		k.FirstSeen = k.LastSeen
	}
	k.BeerLeftUnit = k.DeriveBeerLeftUnit()

	if err := s.writeKeg(tx, k); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return k, nil
}

func newKeg(id string) *Keg {
	return &Keg{
		ID:          id,
		DisplayMode: DisplayWeightPrimary,
		Internal:    map[string]string{},
		Extra:       map[string]string{},
	}
}

// DeriveBeerLeftUnit returns the unit label for the remaining-beer reading.
//
// It is computed from the configured unit, measure mode and keg mode rather
// than taken from the device's own pin 74, because the device can report a
// stale label after a mode change.
func (k *Keg) DeriveBeerLeftUnit() string {
	if k.Unit == nil || (*k.Unit != 1 && *k.Unit != 2) {
		// Without a known unit system, fall back to whatever the device said.
		if k.BeerLeftUnitDevice != nil && *k.BeerLeftUnitDevice != "" {
			return *k.BeerLeftUnitDevice
		}
		return "litre"
	}

	metric := *k.Unit == 1
	weight := k.MeasureUnit != nil && *k.MeasureUnit == 1
	co2 := k.KegMode != nil && *k.KegMode == 2

	switch {
	// CO2 is always measured by weight, whatever the measure mode says.
	case co2 && metric:
		return "kg CO₂"
	case co2:
		return "lbs CO₂"
	case metric && weight:
		return "kg"
	case metric:
		return "litre"
	case weight:
		return "lbs"
	default:
		return "gal"
	}
}
