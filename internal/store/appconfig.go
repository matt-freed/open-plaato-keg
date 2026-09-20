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
	HomePage   string `json:"home_page"`
	TimeFormat string `json:"time_format"`
	Theme      Theme  `json:"theme"`
}

// DefaultAppConfig is what a fresh installation starts with.
func DefaultAppConfig() AppConfig {
	return AppConfig{
		HomePage:   HomePageTapList,
		TimeFormat: TimeFormat12h,
	}
}

const (
	configKeyHomePage   = "home_page"
	configKeyTimeFormat = "time_format"
	configKeyTheme      = "theme"
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
