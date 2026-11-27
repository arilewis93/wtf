package owners

import (
	"context"
	"testing"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/auth"
	"github.com/stretchr/testify/require"
)

// TestLoginAttemptsTouchTheSlotSoARetryIsNotSweptMidHandshake is the fix for
// a real bug: StaleSlotSweeper reaps a never-linked slot purely off
// created_at, and nothing updated that when a slot older than the sweep's
// cutoff was reopened for a fresh linking attempt - so a slot could be
// mid-handshake (reserved, no JID written yet) when the sweep tick fired
// and get removed out from under it. ScopedDevices.LoginDevice and
// LoginDeviceWithCode now touch the slot (store.TouchLinkAttempt) on every
// attempt, resetting the sweeper's cutoff without moving created_at itself.
func TestLoginAttemptsTouchTheSlotSoARetryIsNotSweptMidHandshake(t *testing.T) {
	t.Run("LoginDevice", func(t *testing.T) {
		st, base := staleStore(t)
		ctx := context.Background()
		u, err := st.CreateUser(ctx, "+27825550101")
		require.NoError(t, err)
		require.NoError(t, st.SetOwner(ctx, "d_retry", u.ID))
		svc := NewScopedDevices(&innerStub{}, st)
		ctxU := auth.WithUser(ctx, u)

		// Reopened well past the hour-long cutoff - without the touch this
		// slot would already be eligible for the sweep.
		st.Now = func() time.Time { return base.Add(90 * time.Minute) }
		_, err = svc.LoginDevice(ctxU, "d_retry")
		require.NoError(t, err)

		stale, err := st.NeverLinkedSlotsBefore(ctx, base.Add(60*time.Minute))
		require.NoError(t, err)
		require.Empty(t, stale, "a retried LoginDevice call keeps the slot off the stale list")
	})

	t.Run("LoginDeviceWithCode", func(t *testing.T) {
		st, base := staleStore(t)
		ctx := context.Background()
		u, err := st.CreateUser(ctx, "+27825550101")
		require.NoError(t, err)
		require.NoError(t, st.SetOwner(ctx, "d_retry", u.ID))
		svc := NewScopedDevices(&innerStub{}, st)
		ctxU := auth.WithUser(ctx, u)

		st.Now = func() time.Time { return base.Add(90 * time.Minute) }
		_, err = svc.LoginDeviceWithCode(ctxU, "d_retry", "+27825550199")
		require.NoError(t, err)

		stale, err := st.NeverLinkedSlotsBefore(ctx, base.Add(60*time.Minute))
		require.NoError(t, err)
		require.Empty(t, stale, "a retried LoginDeviceWithCode call keeps the slot off the stale list")
	})

	// A failed ownership check (somebody else's device) must never touch
	// the slot: the touch happens after own() succeeds, never before.
	t.Run("does not touch a device that is not the caller's", func(t *testing.T) {
		st, base := staleStore(t)
		ctx := context.Background()
		owner, err := st.CreateUser(ctx, "+27825550101")
		require.NoError(t, err)
		stranger, err := st.CreateUser(ctx, "+27825550102")
		require.NoError(t, err)
		require.NoError(t, st.SetOwner(ctx, "d_theirs", owner.ID))
		svc := NewScopedDevices(&innerStub{}, st)
		ctxStranger := auth.WithUser(ctx, stranger)

		st.Now = func() time.Time { return base.Add(90 * time.Minute) }
		_, err = svc.LoginDevice(ctxStranger, "d_theirs")
		require.Error(t, err)

		stale, err := st.NeverLinkedSlotsBefore(ctx, base.Add(60*time.Minute))
		require.NoError(t, err)
		require.Len(t, stale, 1, "a stranger's failed attempt does not reset the owner's own cutoff")
	})
}
