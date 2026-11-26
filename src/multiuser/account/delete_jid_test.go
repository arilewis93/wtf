package account

import (
	"context"
	"database/sql"
	"testing"

	domainChatStorage "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/chatstorage"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/chatstorage"
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
