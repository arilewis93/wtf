package store

import (
	"context"
	"time"
)

// neverLinked is the condition for a slot that was reserved and never held a
// WhatsApp identity: no number or name was ever recorded, the phone never
// signed it out, and no link to it was refused (a refusal means a phone did
// scan it). A slot signed out from the phone is not one of these and is kept.
const neverLinked = `last_phone = '' AND last_name = '' AND signed_out_at IS NULL AND last_refusal = ''`

// OwnedSlot is a slot and the user who owns it.
type OwnedSlot struct {
	DeviceID string
	UserID   string
}

// notRetriedSince is paired with neverLinked everywhere the sweep's cutoff is
// checked: a slot reopened for a fresh linking attempt (TouchLinkAttempt)
// stays off the stale list until the cutoff passes again from that attempt,
// even though it has held no WhatsApp identity yet and created_at itself
// does not move.
const notRetriedSince = `(last_attempt_at IS NULL OR last_attempt_at < ?)`

// NeverLinkedSlotsBefore lists slots reserved before cutoff that never held a
// WhatsApp identity and have not been retried since, oldest first. cutoff
// may be in any zone: it is compared as UTC text against created_at and
// last_attempt_at, which the store always writes in UTC.
func (s *Store) NeverLinkedSlotsBefore(ctx context.Context, cutoff time.Time) ([]OwnedSlot, error) {
	cutoff = cutoff.UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT device_id, user_id FROM device_owners WHERE `+neverLinked+` AND created_at < ? AND `+notRetriedSince+`
		 ORDER BY created_at, device_id`, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnedSlot
	for rows.Next() {
		var sl OwnedSlot
		if err := rows.Scan(&sl.DeviceID, &sl.UserID); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

// ExpireNeverLinkedSlot deletes deviceID's owner row only if it is still a
// never-linked slot reserved before cutoff and not retried since, in one
// statement, so a slot that was scanned - or retried - in the meantime is
// never taken. It reports whether it did.
func (s *Store) ExpireNeverLinkedSlot(ctx context.Context, deviceID string, cutoff time.Time) (bool, error) {
	cutoff = cutoff.UTC() // see NeverLinkedSlotsBefore
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM device_owners WHERE device_id = ? AND `+neverLinked+` AND created_at < ? AND `+notRetriedSince,
		deviceID, cutoff, cutoff)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// TouchLinkAttempt records that deviceID's owner just started (or retried) a
// linking attempt, resetting the stale-slot sweeper's cutoff for it without
// moving created_at itself. ScopedDevices.LoginDevice and
// LoginDeviceWithCode call this on every attempt (first or retried), so a
// slot that is mid-handshake when the sweep tick fires is never reaped out
// from under it.
func (s *Store) TouchLinkAttempt(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE device_owners SET last_attempt_at = ? WHERE device_id = ?`, s.now(), deviceID)
	return err
}
