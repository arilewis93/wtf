package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOwnership(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	a, _ := st.CreateUser(ctx, "+27825550100")
	b, _ := st.CreateUser(ctx, "+27825550101")

	require.NoError(t, st.SetOwner(ctx, "d_a1", a.ID))
	require.NoError(t, st.SetOwner(ctx, "d_b1", b.ID))
	require.Error(t, st.SetOwner(ctx, "d_a1", b.ID), "a device has one owner; no silent takeover")

	owner, err := st.OwnerOf(ctx, "d_a1")
	require.NoError(t, err)
	require.Equal(t, a.ID, owner)
	_, err = st.OwnerOf(ctx, "d_nobody")
	require.ErrorIs(t, err, ErrNotFound)

	ids, err := st.DevicesOf(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"d_a1"}, ids)

	n, _ := st.OwnedDeviceCount(ctx)
	require.Equal(t, 2, n)
	require.NoError(t, st.RemoveOwner(ctx, "d_a1"))
	ids, _ = st.DevicesOf(ctx, a.ID)
	require.Empty(t, ids)
}

// TestPhoneClaimedByOtherOwner is account deletion's safety check: a phone
// number can be released by one account and re-paired under a completely
// different one, so "who last recorded this number" must be checked per
// OWNER, not assumed to still be the account being deleted. The rule is
// deliberately simple and fail-closed, with no recency comparison at all
// (device_owners has no column that actually records when a phone was last
// PAIRED, only when a slot was reserved, which is not a real pairing-
// recency signal): any OTHER owner currently holding a non-signed-out slot
// with this number blocks the deletion, full stop, however old that slot's
// own reservation is. Only "signed out" lets the original account's own
// leftover chat history be cleaned up.
func TestPhoneClaimedByOtherOwner(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	a, _ := st.CreateUser(ctx, "+27825550100")
	require.NoError(t, st.SetOwner(ctx, "d_a1", a.ID))
	require.NoError(t, st.RecordIdentity(ctx, "d_a1", "27825550199", "A's number"))

	claimed, err := st.PhoneClaimedByOtherOwner(ctx, a.ID, "27825550199")
	require.NoError(t, err)
	require.False(t, claimed, "nobody else holds it yet")

	b, _ := st.CreateUser(ctx, "+27825550101")

	// Another owner's slot, reserved long before a's own, still blocks the
	// deletion while it is not signed out: no recency comparison at all.
	require.NoError(t, st.SetOwner(ctx, "d_b1", b.ID))
	require.NoError(t, st.RecordIdentity(ctx, "d_b1", "27825550199", "b's phone now"))
	claimed, err = st.PhoneClaimedByOtherOwner(ctx, a.ID, "27825550199")
	require.NoError(t, err)
	require.True(t, claimed, "another owner holds this number and is not signed out")

	// Signed out: no longer an active claim, so cleanup may proceed.
	require.NoError(t, st.MarkSignedOut(ctx, "d_b1", "", ""))
	claimed, err = st.PhoneClaimedByOtherOwner(ctx, a.ID, "27825550199")
	require.NoError(t, err)
	require.False(t, claimed, "the only other row with this phone is signed out")

	claimed, err = st.PhoneClaimedByOtherOwner(ctx, a.ID, "")
	require.NoError(t, err)
	require.False(t, claimed, "an empty number claims nothing")
}

func TestReserveOwnerEnforcesLimitAtomically(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	a, _ := st.CreateUser(ctx, "+27825550100")

	r, err := st.ReserveOwner(ctx, "d_1", a.ID, 1, 0)
	require.NoError(t, err)
	require.Equal(t, Reserved, r)
	r, err = st.ReserveOwner(ctx, "d_2", a.ID, 1, 0)
	require.NoError(t, err)
	require.Equal(t, UserLimit, r, "limit reached")
	_, err = st.OwnerOf(ctx, "d_2")
	require.ErrorIs(t, err, ErrNotFound, "nothing recorded past the limit")
	_, err = st.ReserveOwner(ctx, "d_1", a.ID, 5, 0)
	require.Error(t, err, "a device id is never reserved twice")

	b, _ := st.CreateUser(ctx, "+27825550101")
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, errs := 0, 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := st.ReserveOwner(ctx, fmt.Sprintf("d_b%d", i), b.ID, 2, 0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
			} else if r == Reserved {
				wins++
			}
		}(i)
	}
	wg.Wait()
	require.Zero(t, errs, "losers see the limit, not a lock error")
	require.Equal(t, 2, wins)
	ids, _ := st.DevicesOf(ctx, b.ID)
	require.Len(t, ids, 2)
}

func TestForeignKeysCascadeOnUserDelete(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	var fk int
	require.NoError(t, st.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk, "foreign_keys must be on")

	a, _ := st.CreateUser(ctx, "+27825550100")
	require.NoError(t, st.SetOwner(ctx, "d_a1", a.ID))
	_, err := st.CreateSession(ctx, a.ID, "ua", time.Hour)
	require.NoError(t, err)
	require.NoError(t, st.AddPasskey(ctx, Passkey{CredentialID: "cred1", UserID: a.ID, CredentialJSON: []byte("{}")}))
	require.Error(t, st.SetOwner(ctx, "d_x", "u_nobody"), "an owner row needs a real user")

	_, err = st.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, a.ID)
	require.NoError(t, err)
	for _, table := range []string{"device_owners", "sessions", "passkeys"} {
		var n int
		require.NoError(t, st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE user_id = ?`, a.ID).Scan(&n))
		require.Zero(t, n, table)
	}
}

func TestReserveOwnerServerCap(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	a, _ := st.CreateUser(ctx, "+27825550100")
	b, _ := st.CreateUser(ctx, "+27825550101")

	r, err := st.ReserveOwner(ctx, "d_a1", a.ID, 2, 2)
	require.NoError(t, err)
	require.Equal(t, Reserved, r)
	r, _ = st.ReserveOwner(ctx, "d_b1", b.ID, 2, 2)
	require.Equal(t, Reserved, r)
	r, err = st.ReserveOwner(ctx, "d_b2", b.ID, 2, 2)
	require.NoError(t, err)
	require.Equal(t, ServerFull, r, "two linked, cap two")
	_, err = st.OwnerOf(ctx, "d_b2")
	require.ErrorIs(t, err, ErrNotFound, "nothing recorded past the cap")

	r, _ = st.ReserveOwner(ctx, "d_b2", b.ID, 2, 0)
	require.Equal(t, Reserved, r, "0 means no cap")
	r, _ = st.ReserveOwner(ctx, "d_b3", b.ID, 2, 0)
	require.Equal(t, UserLimit, r, "the user's own limit still applies")
	r, _ = st.ReserveOwner(ctx, "d_b3", b.ID, 2, 3)
	require.Equal(t, UserLimit, r, "both hit: the user's own limit is named")
}
