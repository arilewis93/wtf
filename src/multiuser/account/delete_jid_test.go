package account

import (
	"context"
	"database/sql"
	"testing"
	"time"

	domainChatStorage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chatstorage"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/chatstorage"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/store"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// TestDeleteRemovesChatHistoryKeyedByLastKnownJIDAfterRemoteLogout is the
// fix for a real privacy/data-retention bug: once a device pairs,
// DeviceManager.ensureInstance keys its chat storage by the device's phone
// JID (storageDeviceID := inst.JID()), not by the registry slot id. A
// remote logout clears the live DeviceInstance's JID
// (DeviceInstance.ResetClient), but nothing resets chat storage back to
// keying by slot id. So pairing, then a remote logout, then account
// deletion (which calls Chats.DeleteDeviceData with the SLOT id) used to
// leave the phone's actual chat history on disk, matching nothing deleted.
//
// This uses a real chatstorage.SQLiteRepository rather than the recording
// fake elsewhere in this file, so the assertion is "the chat row is gone",
// not merely "DeleteDeviceData was called with some id".
func TestDeleteRemovesChatHistoryKeyedByLastKnownJIDAfterRemoteLogout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.seed(t, "+27825550100", "d_a1")

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := chatstorage.NewStorageRepository(db).(*chatstorage.SQLiteRepository)
	require.NoError(t, repo.InitializeSchema())
	f.d.Chats = repo

	// Pairing: chat storage is keyed by the device's own phone JID, exactly
	// as DeviceManager.ensureInstance sets it once a live session exists.
	ownJID := "27825550100@s.whatsapp.net"
	require.NoError(t, repo.StoreChat(&domainChatStorage.Chat{DeviceID: ownJID, JID: "27839990000@s.whatsapp.net", Name: "A friend"}))

	// recordSlotEvent records the number against the slot on every
	// events.Connected (RecordIdentity), and keeps it on a remote logout
	// (MarkSignedOut keeps the last-known number while clearing
	// signed_out_at's counterpart on the live instance). The live
	// DeviceInstance's JID is cleared by ResetClient at the same moment -
	// this is the state PurgeDevice sees when deletion runs afterward.
	require.NoError(t, f.st.RecordIdentity(ctx, "d_a1", "27825550100", "Alice"))
	require.NoError(t, f.st.MarkSignedOut(ctx, "d_a1", "", ""))

	require.NoError(t, f.d.Delete(ctx, alice.user.ID))

	chat, err := repo.GetChat("27839990000@s.whatsapp.net")
	require.NoError(t, err)
	require.Nil(t, chat, "the phone's actual chat history must not survive account deletion")
}

// TestDeleteSkipsChatPartitionCurrentlyClaimedByAnotherOwner is the other
// half of the fix above, caught in review: a phone number is not permanently
// bound to one WTF account. Alice's slot once held it and was then signed
// out remotely (exactly the TestDeleteRemovesChatHistoryKeyedByLastKnownJID...
// scenario), but the SAME real number has since been re-paired under a
// completely different account - Bob's slot is reserved more recently than
// Alice's, still connected (never signed out), so it legitimately claims
// that chat partition now. Deleting Alice's account must never reach into
// it. Her own slot and ownership rows are still removed.
func TestDeleteSkipsChatPartitionCurrentlyClaimedByAnotherOwner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	f.st.Now = func() time.Time { return base }
	alice := f.seed(t, "+27825550100", "d_a1")

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := chatstorage.NewStorageRepository(db).(*chatstorage.SQLiteRepository)
	require.NoError(t, repo.InitializeSchema())
	f.d.Chats = repo

	ownJID := "27825550100@s.whatsapp.net"
	require.NoError(t, repo.StoreChat(&domainChatStorage.Chat{DeviceID: ownJID, JID: "27839990000@s.whatsapp.net", Name: "A friend"}))

	require.NoError(t, f.st.RecordIdentity(ctx, "d_a1", "27825550100", "Alice"))
	require.NoError(t, f.st.MarkSignedOut(ctx, "d_a1", "", ""))

	// Re-paired an hour later, under a different account: a NEW
	// device_owners row, so its own created_at is later than Alice's.
	f.st.Now = func() time.Time { return base.Add(time.Hour) }
	bob, err := f.st.CreateUser(ctx, "+27825550101")
	require.NoError(t, err)
	require.NoError(t, f.st.SetOwner(ctx, "d_b1", bob.ID))
	require.NoError(t, f.st.RecordIdentity(ctx, "d_b1", "27825550100", "Bob's phone"))

	require.NoError(t, f.d.Delete(ctx, alice.user.ID))

	// Alice's own account, slot and ownership row are still fully gone.
	_, err = f.st.UserByID(ctx, alice.user.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	aliceOwned, err := f.st.DevicesOf(ctx, alice.user.ID)
	require.NoError(t, err)
	require.Empty(t, aliceOwned)

	// But the shared JID partition - now Bob's, not hers - is untouched.
	chat, err := repo.GetChat("27839990000@s.whatsapp.net")
	require.NoError(t, err)
	require.NotNil(t, chat, "a phone number re-paired by a different account keeps its chat history")

	// And Bob's own slot is completely untouched.
	bobOwned, err := f.st.DevicesOf(ctx, bob.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"d_b1"}, bobOwned)
}

// TestDeleteStillCleansUpWhenAnotherOwnersClaimIsStale is the flip side of
// the test above, also caught in review: PhoneClaimedByOtherOwner must not
// fail open. An old, abandoned row under a different account - however it
// ended up with signed_out_at left NULL, or simply reserved before Alice's
// own slot - must never block Alice's own leftover chat history from being
// cleaned up. Only a genuinely live, more recent claim does that.
func TestDeleteStillCleansUpWhenAnotherOwnersClaimIsStale(t *testing.T) {
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *fixture, ctx context.Context, carolID string)
	}{
		{
			name: "the other owner's slot was itself signed out",
			setup: func(t *testing.T, f *fixture, ctx context.Context, carolID string) {
				t.Helper()
				// Reserved after Alice's own slot, but no longer connected:
				// not an active claim, however recently it was reserved.
				f.st.Now = func() time.Time { return base.Add(time.Hour) }
				require.NoError(t, f.st.SetOwner(ctx, "d_c1", carolID))
				require.NoError(t, f.st.RecordIdentity(ctx, "d_c1", "27825550100", "Carol's old phone"))
				require.NoError(t, f.st.MarkSignedOut(ctx, "d_c1", "", ""))
			},
		},
		{
			name: "the other owner's slot predates Alice's own",
			setup: func(t *testing.T, f *fixture, ctx context.Context, carolID string) {
				t.Helper()
				// Never explicitly signed out (a ghost: its own account may
				// have gone quiet without a clean LoggedOut event), but
				// reserved BEFORE Alice's own slot - not a more recent claim.
				f.st.Now = func() time.Time { return base.Add(-72 * time.Hour) }
				require.NoError(t, f.st.SetOwner(ctx, "d_c1", carolID))
				require.NoError(t, f.st.RecordIdentity(ctx, "d_c1", "27825550100", "Carol's old phone"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.st.Now = func() time.Time { return base }
			alice := f.seed(t, "+27825550100", "d_a1")

			db, err := sql.Open("sqlite3", ":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := chatstorage.NewStorageRepository(db).(*chatstorage.SQLiteRepository)
			require.NoError(t, repo.InitializeSchema())
			f.d.Chats = repo

			ownJID := "27825550100@s.whatsapp.net"
			require.NoError(t, repo.StoreChat(&domainChatStorage.Chat{DeviceID: ownJID, JID: "27839990000@s.whatsapp.net", Name: "A friend"}))
			require.NoError(t, f.st.RecordIdentity(ctx, "d_a1", "27825550100", "Alice"))
			require.NoError(t, f.st.MarkSignedOut(ctx, "d_a1", "", ""))

			carol, err := f.st.CreateUser(ctx, "+27825550199")
			require.NoError(t, err)
			tc.setup(t, f, ctx, carol.ID)

			f.st.Now = func() time.Time { return base.Add(2 * time.Hour) }
			require.NoError(t, f.d.Delete(ctx, alice.user.ID))

			chat, err := repo.GetChat("27839990000@s.whatsapp.net")
			require.NoError(t, err)
			require.Nil(t, chat, "a stale claim under another account must not block Alice's own cleanup")
		})
	}
}
