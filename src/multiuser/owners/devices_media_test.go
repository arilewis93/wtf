package owners

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/auth"
	"github.com/stretchr/testify/require"
)

// TestUnlinkRemovesDownloadedMedia guards against unbounded disk growth:
// unlinking one number (without deleting the whole account) used to remove
// the slot and owner row but leave that device's downloaded media folder on
// disk forever. RemoveDevice must clean it up the same way account deletion
// already does for every device it owns.
func TestUnlinkRemovesDownloadedMedia(t *testing.T) {
	st, _, a, _ := fixture(t)
	root := t.TempDir()
	dir := filepath.Join(root, "d_a1", "2026-11-23")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("jpeg"), 0o600))

	inner := &innerStub{}
	svc := NewScopedDevicesCapped(inner, st, nil, root)
	require.NoError(t, svc.RemoveDevice(auth.WithUser(context.Background(), a), "d_a1"))

	_, err := os.Lstat(filepath.Join(root, "d_a1"))
	require.True(t, os.IsNotExist(err), "media for the unlinked device must be removed")
}

// TestUnlinkSurvivesAFailedMediaRemoval: disk cleanup is best-effort. A device
// whose media folder cannot be removed (replaced by a plain file, so
// os.RemoveAll fails) must still be unlinked: the person already lost the
// number, and a disk problem must not trap them with it still attached.
func TestUnlinkSurvivesAFailedMediaRemoval(t *testing.T) {
	st, _, a, _ := fixture(t)
	root := t.TempDir()
	// A file where a directory is expected: RemoveAll can't recurse into it
	// to remove a child, but removing the path itself still succeeds, so
	// instead make the root itself unwritable to force a real failure.
	blocked := filepath.Join(root, "d_a1")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o600))
	require.NoError(t, os.Chmod(root, 0o500)) // read+execute only: can't unlink entries
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	inner := &innerStub{}
	svc := NewScopedDevicesCapped(inner, st, nil, root)
	err := svc.RemoveDevice(auth.WithUser(context.Background(), a), "d_a1")
	require.NoError(t, err, "unlinking itself must not fail because disk cleanup failed")
}
