package store

import (
	"database/sql"
	"errors"
	"math"
)

// Beverage is an entry in the beverage library, reusable across taps.
type Beverage struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Brewery      string   `json:"brewery"`
	Style        string   `json:"style"`
	ABV          *float64 `json:"abv"`
	IBU          *float64 `json:"ibu"`
	Color        string   `json:"color"`
	Description  string   `json:"description"`
	TastingNotes string   `json:"tasting_notes"`
	OG           *float64 `json:"og"`
	FG           *float64 `json:"fg"`
	SRM          *float64 `json:"srm"`
	Source       string   `json:"source"`
	CreatedAt    int64    `json:"created_at"`
}

const beverageColumns = `id, name, brewery, style, abv, ibu, color, description,
	tasting_notes, og, fg, srm, source, created_at`

func scanBeverage(row interface{ Scan(...any) error }) (*Beverage, error) {
	b := &Beverage{}
	err := row.Scan(&b.ID, &b.Name, &b.Brewery, &b.Style, &b.ABV, &b.IBU, &b.Color,
		&b.Description, &b.TastingNotes, &b.OG, &b.FG, &b.SRM, &b.Source, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ListBeverages returns the library, ordered by name.
func (s *Store) ListBeverages() ([]*Beverage, error) {
	rows, err := s.db.Query(`SELECT ` + beverageColumns + ` FROM beverages ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	beverages := []*Beverage{}
	for rows.Next() {
		b, err := scanBeverage(rows)
		if err != nil {
			return nil, err
		}
		beverages = append(beverages, b)
	}
	return beverages, rows.Err()
}

// GetBeverage returns one beverage, or ErrNotFound.
func (s *Store) GetBeverage(id string) (*Beverage, error) {
	b, err := scanBeverage(s.db.QueryRow(`SELECT `+beverageColumns+` FROM beverages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// SaveBeverage inserts or replaces a beverage.
func (s *Store) SaveBeverage(b *Beverage) error {
	if b.Source == "" {
		b.Source = "manual"
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO beverages (`+beverageColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Name, b.Brewery, b.Style, b.ABV, b.IBU, b.Color, b.Description,
		b.TastingNotes, b.OG, b.FG, b.SRM, b.Source, b.CreatedAt)
	return err
}

// DeleteBeverage removes a beverage.
func (s *Store) DeleteBeverage(id string) error {
	_, err := s.db.Exec("DELETE FROM beverages WHERE id = ?", id)
	return err
}

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
