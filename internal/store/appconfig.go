package store

import (
	"encoding/json"
	"strings"
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
	BgImage          string `json:"bg_image,omitempty"`
	BgOpacity        string `json:"bg_opacity,omitempty"`
}

// AppConfig is the user-editable application configuration.
type AppConfig struct {
	HomePage     string       `json:"home_page"`
	TimeFormat   string       `json:"time_format"`
	DisplayUnits DisplayUnits `json:"display_units"`
	Theme        Theme        `json:"theme"`
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
	}
}

const (
	configKeyHomePage   = "home_page"
	configKeyTimeFormat = "time_format"
	configKeyTheme      = "theme"

	// Two scalar rows rather than one JSON blob: unlike a theme these are a
	// pair of closed enums, so they follow the home_page/time_format pattern.
	configKeyDisplayUnitSystem  = "display_unit_system"
	configKeyDisplayUnitMeasure = "display_unit_measure"
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
		case configKeyTheme:
			var theme Theme
			if err := json.Unmarshal([]byte(value), &theme); err == nil {
				cfg.Theme = theme
			}
		}
	}
	return cfg, rows.Err()
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
