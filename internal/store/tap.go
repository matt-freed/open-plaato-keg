package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

// DeviceIDMaxLen bounds the identifier an open-tap display reports. The
// firmware sends a short id and longer values would never match.
const DeviceIDMaxLen = 6

// Tap is one configured tap on the tap list.
type Tap struct {
	ID string `json:"id"`
	// TapNumber orders the tap list; nil sorts last.
	TapNumber      *int     `json:"tap_number"`
	Name           string   `json:"name"`
	Brewery        string   `json:"brewery"`
	Style          string   `json:"style"`
	ABV            *float64 `json:"abv"`
	IBU            *float64 `json:"ibu"`
	Color          string   `json:"color"`
	Description    string   `json:"description"`
	TastingNotes   string   `json:"tasting_notes"`
	ExpirationDate string   `json:"expiration_date"`
	// KegID links the tap to a keg, so the tap list can show what is left.
	KegID string `json:"keg_id"`
	// HandleImage is the filename of an uploaded tap handle image.
	HandleImage string `json:"handle_image"`
	// DeviceID identifies an open-tap ESP32 display bound to this tap.
	DeviceID string `json:"device_id"`
}

// DefaultTapColor is the accent used when a tap has no colour set.
const DefaultTapColor = "#c9a849"

const tapColumns = `id, tap_number, name, brewery, style, abv, ibu, color,
	description, tasting_notes, expiration_date, keg_id, handle_image, device_id`

func scanTap(row interface{ Scan(...any) error }) (*Tap, error) {
	t := &Tap{}
	err := row.Scan(&t.ID, &t.TapNumber, &t.Name, &t.Brewery, &t.Style, &t.ABV, &t.IBU,
		&t.Color, &t.Description, &t.TastingNotes, &t.ExpirationDate, &t.KegID,
		&t.HandleImage, &t.DeviceID)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListTaps returns every tap, ordered by tap number with unnumbered taps last.
func (s *Store) ListTaps() ([]*Tap, error) {
	rows, err := s.db.Query(`SELECT ` + tapColumns + ` FROM taps
		ORDER BY tap_number IS NULL, tap_number, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	taps := []*Tap{}
	for rows.Next() {
		t, err := scanTap(rows)
		if err != nil {
			return nil, err
		}
		taps = append(taps, t)
	}
	return taps, rows.Err()
}

// GetTap returns one tap, or ErrNotFound.
func (s *Store) GetTap(id string) (*Tap, error) {
	t, err := scanTap(s.db.QueryRow(`SELECT `+tapColumns+` FROM taps WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// GetTapByDeviceID finds the tap bound to an open-tap display. The lookup is
// case-insensitive because the firmware's casing is not guaranteed.
func (s *Store) GetTapByDeviceID(deviceID string) (*Tap, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, ErrNotFound
	}
	t, err := scanTap(s.db.QueryRow(
		`SELECT `+tapColumns+` FROM taps WHERE device_id <> '' AND lower(device_id) = lower(?)`,
		deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// SaveTap inserts or replaces a tap.
func (s *Store) SaveTap(t *Tap) error {
	if t.Color == "" {
		t.Color = DefaultTapColor
	}
	if len(t.DeviceID) > DeviceIDMaxLen {
		t.DeviceID = t.DeviceID[:DeviceIDMaxLen]
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO taps (`+tapColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.TapNumber, t.Name, t.Brewery, t.Style, t.ABV, t.IBU, t.Color,
		t.Description, t.TastingNotes, t.ExpirationDate, t.KegID, t.HandleImage, t.DeviceID)
	return err
}

// DeleteTap removes a tap.
func (s *Store) DeleteTap(id string) error {
	_, err := s.db.Exec("DELETE FROM taps WHERE id = ?", id)
	return err
}

// NewID returns a short random identifier for a new record.
func NewID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail in practice; a readable panic beats
		// silently issuing colliding ids.
		panic("open-plaato-keg: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}
