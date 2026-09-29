package store

import "testing"

func TestNormalizeDate(t *testing.T) {
	for _, tc := range []struct{ in, layout, want string }{
		{"", DateLayout, ""},
		{"  ", KegDateLayout, ""},
		{"2026-09-03", DateLayout, "2026-09-03"},
		{"2026-9-3", DateLayout, "2026-09-03"},
		{"03.09.2026", DateLayout, "2026-09-03"},
		{"3.9.2026", KegDateLayout, "03.09.2026"},
		{" 03.09.26 ", KegDateLayout, "03.09.2026"},
		{"2026-09-03", KegDateLayout, "03.09.2026"},
		{"29.02.2028", DateLayout, "2028-02-29"}, // a leap day
	} {
		got, err := NormalizeDate(tc.in, tc.layout)
		if err != nil || got != tc.want {
			t.Errorf("NormalizeDate(%q, %q) = %q, %v; want %q", tc.in, tc.layout, got, err, tc.want)
		}
	}

	for _, in := range []string{"Labor Day", "03/04/2026", "31.02.2026", "29.02.2026", "2026-13-01", "12.01.2025 noon"} {
		if got, err := NormalizeDate(in, DateLayout); err == nil {
			t.Errorf("NormalizeDate(%q) = %q, want an error", in, got)
		}
	}
}
