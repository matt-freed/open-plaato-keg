package store

import "math"

// EstimateABV derives alcohol by volume from original and final gravity.
//
// Gravities may be given either as specific gravity (1.050) or as gravity
// points (1050), which is how the Plaato hardware reports them.
//
// The result is rounded to two decimals: the formula is an approximation, and
// the unrounded value carries floating point noise into the API.
func EstimateABV(og, fg float64) float64 {
	if og > 100 {
		og /= 1000
	}
	if fg > 100 {
		fg /= 1000
	}
	return math.Round((og-fg)*131.25*100) / 100
}
