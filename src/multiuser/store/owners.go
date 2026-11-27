package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) SetOwner(ctx context.Context, deviceID, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO device_owners (device_id, user_id, created_at) VALUES (?, ?, ?)`, deviceID, userID, s.now())
	return err
}

func (s *Store) OwnerOf(ctx context.Context, deviceID string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM device_owners WHERE device_id = ?`, deviceID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return userID, err
}

// PhoneClaimedByOtherOwner reports whether some OTHER account's slot is now
// the one actively holding phone (bare digits, as last_phone stores it): not
// signed out. A phone number is not permanently bound to one account: it
// can be released by one and re-paired under a completely different one
// later. Account deletion calls this before clearing chat data under that
// number's JID, because that chat partition then belongs to whoever holds
// the number now, not to whoever used to - deleting it on behalf of the
// account being removed would destroy a different, live account's current
// chat history.
//
// This is deliberately fail-closed and nothing more: device_owners has no
// column that actually records when a phone was last PAIRED (created_at is
// only when the slot was reserved, not a real pairing-recency signal, and a
// version of this check that compared it was rejected in review three times
// running for exactly that reason). "Not signed out" is the one real,
// durable signal of an active claim this store has. The cost is a narrow
// edge case: another owner's row that is itself abandoned but never got
// marked signed out reads as a live claim too, so the account being
// deleted's own stale chat history can linger a little longer than
// strictly necessary - a minor retention nicety, never a cross-tenant
// destruction risk, which is the property that actually matters here.
func (s *Store) PhoneClaimedByOtherOwner(ctx context.Context, excludeUserID, phone string) (bool, error) {
	if phone == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM device_owners WHERE last_phone = ? AND user_id != ? AND signed_out_at IS NULL`,
		phone, excludeUserID).Scan(&n)
	return n > 0, err
}

func (s *Store) DevicesOf(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT device_id FROM device_owners WHERE user_id = ? ORDER BY created_at, device_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) RemoveOwner(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM device_owners WHERE device_id = ?`, deviceID)
	return err
}

func (s *Store) OwnedDeviceCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_owners`).Scan(&n)
	return n, err
}

// Reservation is ReserveOwner's answer.
type Reservation int

const (
	Reserved   Reservation = iota + 1 // the slot is the user's
	UserLimit                         // the user already has maxPerUser numbers
	ServerFull                        // the server already has maxTotal numbers
)

// ReserveOwner records userID as deviceID's owner only while the user owns
// fewer than maxPerUser devices and, when maxTotal > 0, the server holds
// fewer than maxTotal. Both counts and the insert are one SQL statement, and
// SQLite takes the write lock before the statement reads, so concurrent calls
// can never take a user past their limit or the server past its cap. Every
// owner row counts, including a slot whose QR code was never scanned: it
// holds a reservation.
//
// When nothing was inserted, a second read names the limit that was hit, for
// the message only; the decision was already made atomically. When both
// were hit, the user's own limit is named.
func (s *Store) ReserveOwner(ctx context.Context, deviceID, userID string, maxPerUser, maxTotal int) (Reservation, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO device_owners (device_id, user_id, created_at)
		 SELECT ?, ?, ?
		 WHERE (SELECT COUNT(*) FROM device_owners WHERE user_id = ?) < ?
		   AND (? = 0 OR (SELECT COUNT(*) FROM device_owners) < ?)`,
		deviceID, userID, s.now(), userID, maxPerUser, maxTotal, maxTotal)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 1 {
		return Reserved, nil
	}
	var mine int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_owners WHERE user_id = ?`, userID).Scan(&mine); err != nil {
		return 0, err
	}
	if mine >= maxPerUser {
		return UserLimit, nil
	}
	return ServerFull, nil
}

// SetLabel names one of the user's own devices; "" clears the name. The label
// must already be validated by the caller (owners.NormalizeLabel). ErrNotFound
// unless userID owns deviceID, so nobody can name another user's device.
func (s *Store) SetLabel(ctx context.Context, userID, deviceID, label string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE device_owners SET label = ? WHERE device_id = ? AND user_id = ?`, label, deviceID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LabelsOf maps each of the user's devices to its label ("" when unnamed), in
// one query.
func (s *Store) LabelsOf(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT device_id, label FROM device_owners WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		out[id] = label
	}
	return out, rows.Err()
}
