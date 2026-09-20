package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

func (s *Server) handleGetHomePage(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"home_page": cfg.HomePage})
}

func (s *Server) handleSetHomePage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HomePage string `json:"home_page"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	page := store.NormalizeHomePage(req.HomePage)
	if err := s.store.SetHomePage(page); err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "home_page": page})
}

func (s *Server) handleGetTimeFormat(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"time_format": cfg.TimeFormat})
}

func (s *Server) handleSetTimeFormat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TimeFormat string `json:"time_format"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	format := store.NormalizeTimeFormat(req.TimeFormat)
	if err := s.store.SetTimeFormat(format); err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "time_format": format})
}

func (s *Server) handleGetTheme(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetAppConfig()
	if err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, cfg.Theme)
}

func (s *Server) handleSetTheme(w http.ResponseWriter, r *http.Request) {
	var theme store.Theme
	if !decodeJSON(w, r, &theme) {
		return
	}
	if err := s.store.SetTheme(theme); err != nil {
		writeStoreError(w, err, "configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "theme": theme})
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
	writeCSSVar(&b, "--font-family", theme.FontFamily)
	writeCSSVar(&b, "--taplist-title-font", theme.TapListTitleFont)
	writeCSSVar(&b, "--taplist-body-font", theme.TapListBodyFont)
	if opacity := parseOpacity(theme.BgOpacity); opacity != "" {
		writeCSSVar(&b, "--bg-opacity", opacity)
	}
	b.WriteString("}\n")

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	// The theme changes from the settings page and must take effect on reload.
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, b.String())
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

func parseOpacity(value string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || f < 0 || f > 1 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
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
	case "serif", "sans-serif", "monospace", "cursive", "fantasy", "system-ui", "inherit":
		return ""
	}
	return first
}
