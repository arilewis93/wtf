package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Behaviour: only slots that never held a number, and are older than the
// cutoff, are listed and can be expired; a slot signed out from the phone, a
// refused one, a connected one and a fresh one are all kept.
func TestNeverLinkedSlotsAreTheOnlyOnesExpired(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return base }
	u, err := st.CreateUser(ctx, "+27825550101")
	require.NoError(t, err)
	for _, id := range []string{"d_stale", "d_signed_out", "d_refused", "d_linked"} {
		require.NoError(t, st.SetOwner(ctx, id, u.ID))
	}
	require.NoError(t, st.MarkSignedOut(ctx, "d_signed_out", "27825550101", ""))
	require.NoError(t, st.RecordRefusal(ctx, "d_refused", RefusalDuplicate))
	require.NoError(t, st.RecordIdentity(ctx, "d_linked", "27825550199", "Shop"))
	st.Now = func() time.Time { return base.Add(50 * time.Minute) }
	require.NoError(t, st.SetOwner(ctx, "d_fresh", u.ID))

	cutoff := base.Add(30 * time.Minute)
	got, err := st.NeverLinkedSlotsBefore(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, []OwnedSlot{{DeviceID: "d_stale", UserID: u.ID}}, got)

	for _, id := range []string{"d_signed_out", "d_refused", "d_linked", "d_fresh"} {
		ok, err := st.ExpireNeverLinkedSlot(ctx, id, cutoff)
		require.NoError(t, err)
		require.False(t, ok, id)
	}
	ok, err := st.ExpireNeverLinkedSlot(ctx, "d_stale", cutoff)
	require.NoError(t, err)
	require.True(t, ok)
	ids, err := st.DevicesOf(ctx, u.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"d_signed_out", "d_refused", "d_linked", "d_fresh"}, ids)
}

// Behaviour: a never-linked slot retried after it is already older than the
// sweep's cutoff is not swept, even though created_at itself never changes
// (that column also orders DevicesOf's "oldest first" list, which a retry
// must not reshuffle). TouchLinkAttempt is called on every retried linking
// attempt (ScopedDevices.LoginDevice/LoginDeviceWithCode), so a slot that is
// mid-handshake when the sweep tick fires is not reaped out from under it.
func TestTouchLinkAttemptKeepsARetriedSlotOffTheStaleList(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return base }
	u, err := st.CreateUser(ctx, "+27825550101")
	require.NoError(t, err)
	require.NoError(t, st.SetOwner(ctx, "d_retry", u.ID))

	// More than an hour later - without a retry this slot would already be
	// stale, exactly like TestNeverLinkedSlotsAreTheOnlyOnesExpired's
	// "d_stale" case - but it is retried right now.
	st.Now = func() time.Time { return base.Add(90 * time.Minute) }
	require.NoError(t, st.TouchLinkAttempt(ctx, "d_retry"))

	cutoff := base.Add(60 * time.Minute)
	got, err := st.NeverLinkedSlotsBefore(ctx, cutoff)
	require.NoError(t, err)
	require.Empty(t, got, "a retried slot's cutoff resets even though created_at itself stays")

	ok, err := st.ExpireNeverLinkedSlot(ctx, "d_retry", cutoff)
	require.NoError(t, err)
	require.False(t, ok, "the retried slot is not swept mid-handshake")

	// created_at is untouched: the "oldest first" order DevicesOf reads from
	// it must not reshuffle just because the person retried.
	require.NoError(t, st.SetOwner(ctx, "d_other", u.ID))
	ids, err := st.DevicesOf(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"d_retry", "d_other"}, ids)
}

// Mechanism: a slot that gets a number between the listing and the expiry is
// not taken (the expiry re-checks in the same statement as the delete).
func TestExpiryRechecksTheSlot(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return base }
	u, err := st.CreateUser(ctx, "+27825550101")
	require.NoError(t, err)
	require.NoError(t, st.SetOwner(ctx, "d_x", u.ID))
	cutoff := base.Add(time.Hour)
	got, err := st.NeverLinkedSlotsBefore(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NoError(t, st.RecordIdentity(ctx, "d_x", "27825550101", ""))
	ok, err := st.ExpireNeverLinkedSlot(ctx, "d_x", cutoff)
	require.NoError(t, err)
	require.False(t, ok)
}
