package store

import "time"

// TapHandle is an uploaded tap handle image.
type TapHandle struct {
	Filename   string `json:"filename"`
	UploadedAt int64  `json:"uploaded_at"`
}

// ListTapHandles returns every uploaded handle, newest first.
func (s *Store) ListTapHandles() ([]TapHandle, error) {
	rows, err := s.db.Query("SELECT filename, uploaded_at FROM tap_handles ORDER BY uploaded_at DESC, filename")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	handles := []TapHandle{}
	for rows.Next() {
		var h TapHandle
		if err := rows.Scan(&h.Filename, &h.UploadedAt); err != nil {
			return nil, err
		}
		handles = append(handles, h)
	}
	return handles, rows.Err()
}

// AddTapHandle records an uploaded handle.
func (s *Store) AddTapHandle(filename string, at time.Time) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO tap_handles (filename, uploaded_at) VALUES (?, ?)",
		filename, at.Unix())
	return err
}

// HasTapHandle reports whether a handle with this filename was uploaded.
func (s *Store) HasTapHandle(filename string) (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM tap_handles WHERE filename = ?", filename).Scan(&n)
	return n > 0, err
}

// DeleteTapHandle forgets a handle and clears it from any tap using it.
func (s *Store) DeleteTapHandle(filename string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM tap_handles WHERE filename = ?", filename); err != nil {
		return err
	}
	// Leaving the reference behind would show a broken image on the tap list.
	if _, err := tx.Exec("UPDATE taps SET handle_image = '' WHERE handle_image = ?", filename); err != nil {
		return err
	}
	return tx.Commit()
}
