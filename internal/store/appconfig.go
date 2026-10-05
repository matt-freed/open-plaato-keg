package store

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/matt-freed/open-plaato-keg/internal/units"
)

// Home pages the UI can open on.
const (
	HomePageTapList = "taplist"
	HomePageKegs    = "kegs"
)

// Clock formats the UI can display times in.
const (
	TimeFormat12h = "12h"
	TimeFormat24h = "24h"
)

// Unit systems the UI can present readings in.
const (
	DisplaySystemDevice = "device" // follow whatever the scale is set to
	DisplaySystemMetric = "metric"
	DisplaySystemUS     = "us"
)

// How a remaining-beer reading is presented.
const (
	DisplayMeasureDevice = "device"
	DisplayMeasureWeight = "weight"
	DisplayMeasureVolume = "volume"
)

// Which figure a keg graphic shows large on the Kegs page and the tap list.
const (
	AmountDisplayAmount  = "amount"  // the amount left, in the display units
	AmountDisplayPercent = "percent" // the percentage left
)

// Units the minimum pour can be entered in.
const (
	MinPourUnitOz = "oz" // US fluid ounces
	MinPourUnitMl = "ml"
)

// MinPour is the smallest drop in a pouring window that counts as a pour.
//
// It is applied when a pour ends and never again, so changing it affects
// future pours only.
type MinPour struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// DefaultMinPour is a small taster, and below any real glass.
var DefaultMinPour = MinPour{Value: 4, Unit: MinPourUnitOz}

// In returns the minimum in a remaining-beer unit such as "lbs" or "litre".
// Weight units pick up the same litre-per-kilogram assumption as every other
// conversion in package units.
func (m MinPour) In(unit string) float64 {
	if m.Unit == MinPourUnitMl {
		return units.ConvertAmount(m.Value/1000, "litre", unit)
	}
	return units.ConvertAmount(m.Value/128, "gal", unit)
}

// NormalizeMinPour maps any input onto a usable minimum: an unknown unit
// becomes ounces, and a negative or non-finite value becomes zero.
func NormalizeMinPour(m MinPour) MinPour {
	unit := MinPourUnitOz
	if strings.EqualFold(strings.TrimSpace(m.Unit), MinPourUnitMl) {
		unit = MinPourUnitMl
	}
	value := m.Value
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		value = 0
	}
	return MinPour{Value: value, Unit: unit}
}

// DisplayUnits is a presentation preference only.
//
// Changing the unit on the scale itself changes what the device reports, which
// changes what is persisted here and what is forwarded to BarHelper. This
// setting changes none of that: it only decides how the browser renders a
// reading.
type DisplayUnits struct {
	System  string `json:"system"`
	Measure string `json:"measure"`
}

// FollowsDevice reports whether both axes are left to the device, which is the
// default and renders exactly as the UI did before this setting existed.
func (d DisplayUnits) FollowsDevice() bool {
	return d.System == DisplaySystemDevice && d.Measure == DisplayMeasureDevice
}

// Theme holds the appearance settings applied through /theme.css.
//
// Only these keys are accepted: they are interpolated into a stylesheet, so an
// open-ended map would let a settings write inject arbitrary CSS.
type Theme struct {
	AccentColor      string `json:"accent_color,omitempty"`
	BgColor          string `json:"bg_color,omitempty"`
	CardBg           string `json:"card_bg,omitempty"`
	TextColor        string `json:"text_color,omitempty"`
	FontFamily       string `json:"font_family,omitempty"`
	TapListTitleFont string `json:"taplist_title_font,omitempty"`
	TapListBodyFont  string `json:"taplist_body_font,omitempty"`
}

// AppConfig is the user-editable application configuration.
type AppConfig struct {
	HomePage     string       `json:"home_page"`
	TimeFormat   string       `json:"time_format"`
	DisplayUnits DisplayUnits `json:"display_units"`
	// AmountDisplay applies to every keg. A CO2 scale always shows its amount.
	AmountDisplay string  `json:"amount_display"`
	MinPour       MinPour `json:"min_pour"`
	Theme         Theme   `json:"theme"`
}

// DefaultAppConfig is what a fresh installation starts with.
func DefaultAppConfig() AppConfig {
	return AppConfig{
		HomePage:   HomePageTapList,
		TimeFormat: TimeFormat12h,
		DisplayUnits: DisplayUnits{
			System:  DisplaySystemDevice,
			Measure: DisplayMeasureDevice,
		},
		AmountDisplay: AmountDisplayAmount,
		MinPour:       DefaultMinPour,
	}
}

const (
	configKeyHomePage      = "home_page"
	configKeyTimeFormat    = "time_format"
	configKeyTheme         = "theme"
	configKeyAmountDisplay = "amount_display"

	// Two scalar rows rather than one JSON blob: unlike a theme these are a
	// pair of closed enums, so they follow the home_page/time_format pattern.
	configKeyDisplayUnitSystem  = "display_unit_system"
	configKeyDisplayUnitMeasure = "display_unit_measure"

	configKeyMinPourValue = "min_pour_value"
	configKeyMinPourUnit  = "min_pour_unit"
)

// GetAppConfig returns the stored configuration, filling in defaults.
func (s *Store) GetAppConfig() (AppConfig, error) {
	cfg := DefaultAppConfig()

	rows, err := s.db.Query("SELECT key, value FROM app_config")
	if err != nil {
		return cfg, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return cfg, err
		}
		switch key {
		case configKeyHomePage:
			cfg.HomePage = NormalizeHomePage(value)
		case configKeyTimeFormat:
			cfg.TimeFormat = NormalizeTimeFormat(value)
		case configKeyDisplayUnitSystem:
			cfg.DisplayUnits.System = NormalizeDisplaySystem(value)
		case configKeyDisplayUnitMeasure:
			cfg.DisplayUnits.Measure = NormalizeDisplayMeasure(value)
		case configKeyAmountDisplay:
			cfg.AmountDisplay = NormalizeAmountDisplay(value)
		case configKeyMinPourValue:
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				cfg.MinPour.Value = v
			}
		case configKeyMinPourUnit:
			cfg.MinPour.Unit = value
		case configKeyTheme:
			var theme Theme
			if err := json.Unmarshal([]byte(value), &theme); err == nil {
				cfg.Theme = theme
			}
		}
	}
	if err := rows.Err(); err != nil {
		return cfg, err
	}
	cfg.MinPour = NormalizeMinPour(cfg.MinPour)
	return cfg, nil
}

// minPourTx reads the minimum pour inside a transaction. The store holds a
// single connection, so trackPour cannot call GetAppConfig while its
// transaction is open.
func minPourTx(q querier) (MinPour, error) {
	m := DefaultMinPour
	rows, err := q.Query("SELECT key, value FROM app_config WHERE key IN (?, ?)",
		configKeyMinPourValue, configKeyMinPourUnit)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return m, err
		}
		switch key {
		case configKeyMinPourValue:
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				m.Value = v
			}
		case configKeyMinPourUnit:
			m.Unit = value
		}
	}
	return NormalizeMinPour(m), rows.Err()
}

// SetHomePage stores which page the UI opens on.
func (s *Store) SetHomePage(page string) error {
	return s.setConfig(configKeyHomePage, NormalizeHomePage(page))
}

// SetTimeFormat stores the clock format.
func (s *Store) SetTimeFormat(format string) error {
	return s.setConfig(configKeyTimeFormat, NormalizeTimeFormat(format))
}

// SetDisplayUnits stores how the UI should present readings.
//
// The two axes are written separately, so a failure between them leaves one
// applied. That is harmless for a settings write: the next save fixes it, and
// neither value affects stored or forwarded data.
func (s *Store) SetDisplayUnits(u DisplayUnits) error {
	if err := s.setConfig(configKeyDisplayUnitSystem, NormalizeDisplaySystem(u.System)); err != nil {
		return err
	}
	return s.setConfig(configKeyDisplayUnitMeasure, NormalizeDisplayMeasure(u.Measure))
}

// SetAmountDisplay stores which figure the keg graphics show large.
func (s *Store) SetAmountDisplay(display string) error {
	return s.setConfig(configKeyAmountDisplay, NormalizeAmountDisplay(display))
}

// SetMinPour stores the minimum pour. Both rows are written in one
// transaction, since a value read with the wrong unit would be off by a factor
// of thirty.
func (s *Store) SetMinPour(m MinPour) error {
	m = NormalizeMinPour(m)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		configKeyMinPourValue: strconv.FormatFloat(m.Value, 'f', -1, 64),
		configKeyMinPourUnit:  m.Unit,
	} {
		if _, err := tx.Exec("INSERT OR REPLACE INTO app_config (key, value) VALUES (?, ?)", key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetTheme stores the appearance settings.
func (s *Store) SetTheme(theme Theme) error {
	encoded, err := json.Marshal(theme)
	if err != nil {
		return err
	}
	return s.setConfig(configKeyTheme, string(encoded))
}

func (s *Store) setConfig(key, value string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO app_config (key, value) VALUES (?, ?)", key, value)
	return err
}

// NormalizeHomePage maps any input onto a supported home page.
func NormalizeHomePage(page string) string {
	if strings.EqualFold(strings.TrimSpace(page), HomePageKegs) {
		return HomePageKegs
	}
	return HomePageTapList
}

// NormalizeTimeFormat maps any input onto a supported clock format.
func NormalizeTimeFormat(format string) string {
	if strings.TrimSpace(format) == TimeFormat24h {
		return TimeFormat24h
	}
	return TimeFormat12h
}

// NormalizeDisplaySystem maps any input onto a supported unit system.
//
// Anything unrecognised falls back to following the device, which is the safe
// direction: an unreadable setting shows the reading as the scale reports it
// rather than converting it from a guess.
func NormalizeDisplaySystem(system string) string {
	switch strings.ToLower(strings.TrimSpace(system)) {
	case DisplaySystemMetric:
		return DisplaySystemMetric
	case DisplaySystemUS:
		return DisplaySystemUS
	}
	return DisplaySystemDevice
}

// NormalizeDisplayMeasure maps any input onto a supported measure.
func NormalizeDisplayMeasure(measure string) string {
	switch strings.ToLower(strings.TrimSpace(measure)) {
	case DisplayMeasureWeight:
		return DisplayMeasureWeight
	case DisplayMeasureVolume:
		return DisplayMeasureVolume
	}
	return DisplayMeasureDevice
}

// NormalizeAmountDisplay maps any input onto a supported amount display,
// falling back to the amount left.
func NormalizeAmountDisplay(display string) string {
	if strings.EqualFold(strings.TrimSpace(display), AmountDisplayPercent) {
		return AmountDisplayPercent
	}
	return AmountDisplayAmount
}
