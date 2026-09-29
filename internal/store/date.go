package store

import (
	"fmt"
	"strings"
	"time"
)

// Layouts a date is written in.
const (
	// DateLayout is how a tap's kegged date is stored: ISO, which also sorts
	// and is what a browser date picker produces.
	DateLayout = "2006-01-02"
	// KegDateLayout is how a keg's own date is stored and sent to the device:
	// day first, matching the Plaato app.
	KegDateLayout = "02.01.2006"
)

// dateInputs are the forms a typed date is accepted in. Slash dates are left
// out on purpose: 03/04/2025 could be either month.
var dateInputs = []string{"2006-1-2", "2.1.2006", "2.1.06"}

// parseDate reads a date in any of dateInputs.
func parseDate(value string) (time.Time, bool) {
	for _, layout := range dateInputs {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// NormalizeDate checks a date typed as YYYY-MM-DD, DD.MM.YYYY or DD.MM.YY and
// returns it in layout. Empty input means no date and is returned as "".
func NormalizeDate(value, layout string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	t, ok := parseDate(value)
	if !ok {
		return "", fmt.Errorf("%q is not a date; use YYYY-MM-DD or DD.MM.YYYY", value)
	}
	return t.Format(layout), nil
}
