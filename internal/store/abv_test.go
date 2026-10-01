package store

import "testing"

// Gravities arrive both as specific gravity and as the points the hardware
// reports.
func TestEstimateABV(t *testing.T) {
	tests := []struct {
		og, fg, want float64
	}{
		{1.050, 1.010, 5.25},
		{1050, 1010, 5.25},
		{1.055, 1.015, 5.25},
	}
	for _, tt := range tests {
		// The result is rounded, so it must match exactly rather than
		// approximately — an unrounded value carries floating point noise into
		// the API.
		if got := EstimateABV(tt.og, tt.fg); got != tt.want {
			t.Errorf("EstimateABV(%v, %v) = %v, want %v", tt.og, tt.fg, got, tt.want)
		}
	}
}
