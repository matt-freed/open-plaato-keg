package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleUpdateConfig changes any of the settings at once. Each key present is
// replaced whole, so display_units, min_pour and theme are sent complete; a
// key left out keeps its stored value. Values are normalized as they are
// stored, so the response is the configuration as it now stands.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var fields map[string]json.RawMessage
	if !decodeJSON(w, r, &fields) {
		return
	}
	if len(fields) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "name at least one setting to change")
		return
	}

	var patch store.AppConfig
	for name, raw := range fields {
		var dst any
		switch name {
		case "home_page":
			dst = &patch.HomePage
		case "time_format":
			dst = &patch.TimeFormat
		case "display_units":
			dst = &patch.DisplayUnits
		case "amount_display":
			dst = &patch.AmountDisplay
		case "min_pour":
			dst = &patch.MinPour
		case "theme":
			dst = &patch.Theme
		default:
			writeError(w, http.StatusBadRequest, "invalid_value", name+" is not a setting")
			return
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_value", name+" has an invalid value")
			return
		}
	}

	cfg, err := s.store.UpdateAppConfig(func(c *store.AppConfig) {
		for name := range fields {
			switch name {
			case "home_page":
				c.HomePage = patch.HomePage
			case "time_format":
				c.TimeFormat = patch.TimeFormat
			case "display_units":
				c.DisplayUnits = patch.DisplayUnits
			case "amount_display":
				c.AmountDisplay = patch.AmountDisplay
			case "min_pour":
				c.MinPour = patch.MinPour
			case "theme":
				c.Theme = patch.Theme
			}
		}
	})
	if err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleThemeCSS renders the stored theme as a stylesheet of custom
// properties, which style.css then consumes.
func (s *Server) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		cfg = store.DefaultAppConfig()
	}
	theme := cfg.Theme

	var b strings.Builder
	if font := googleFontImport(theme); font != "" {
		b.WriteString(font)
	}
	b.WriteString(":root {\n")
	writeCSSVar(&b, "--accent-color", theme.AccentColor)
	writeCSSVar(&b, "--bg-color", theme.BgColor)
	writeCSSVar(&b, "--card-bg", theme.CardBg)
	writeCSSVar(&b, "--text-color", theme.TextColor)
	writeCSSVar(&b, "--font-family", fontStack(theme.FontFamily))
	writeCSSVar(&b, "--taplist-title-font", fontStack(theme.TapListTitleFont))
	writeCSSVar(&b, "--taplist-body-font", fontStack(theme.TapListBodyFont))
	b.WriteString("}\n")

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	// The theme changes from the settings page and must take effect on reload.
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, b.String())
}

// systemFontStack is the default UI font, and what "System" selects.
const systemFontStack = "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"

// fontStack turns a stored font setting into a complete stack, so a font that
// fails to load falls back to the system font rather than the browser's serif.
// The settings page stores a bare family name, or "System" for the default;
// a value that is already a stack is left alone.
func fontStack(name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ""
	case strings.EqualFold(name, "system"):
		return systemFontStack
	case strings.Contains(name, ","):
		return name
	}
	// Single quotes, since cssValue rejects double quotes. A name that could
	// break out of them is dropped.
	if strings.ContainsAny(name, `'"`) {
		return ""
	}
	return "'" + name + "', " + systemFontStack
}

// cssValue rejects anything that could terminate the declaration and inject
// further CSS. Values reach here straight from the settings form.
func cssValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, ";{}<>\\\"") || strings.Contains(value, "*/") {
		return ""
	}
	// url() would let a theme fetch a remote resource on every page load.
	if strings.Contains(strings.ToLower(value), "url(") {
		return ""
	}
	return value
}

func writeCSSVar(b *strings.Builder, name, value string) {
	if v := cssValue(value); v != "" {
		fmt.Fprintf(b, "  %s: %s;\n", name, v)
	}
}

// googleFontImport builds the @import for whichever fonts the theme names.
func googleFontImport(theme store.Theme) string {
	seen := map[string]bool{}
	var families []string
	for _, name := range []string{theme.FontFamily, theme.TapListTitleFont, theme.TapListBodyFont} {
		family := fontFamilyName(name)
		if family == "" || seen[family] {
			continue
		}
		seen[family] = true
		families = append(families, "family="+url.QueryEscape(family)+":wght@400;600;700")
	}
	if len(families) == 0 {
		return ""
	}
	return "@import url('https://fonts.googleapis.com/css2?" +
		strings.Join(families, "&") + "&display=swap');\n"
}

// fontFamilyName extracts the first family from a CSS font stack, keeping only
// names that are safe to put in a URL.
func fontFamilyName(stack string) string {
	first, _, _ := strings.Cut(stack, ",")
	first = strings.TrimSpace(strings.Trim(strings.TrimSpace(first), `'"`))
	if first == "" {
		return ""
	}
	for _, r := range first {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit && r != ' ' && r != '-' {
			return ""
		}
	}
	// Generic keywords are not Google Fonts.
	switch strings.ToLower(first) {
	case "serif", "sans-serif", "monospace", "cursive", "fantasy", "system-ui", "inherit", "system":
		return ""
	}
	return first
}
