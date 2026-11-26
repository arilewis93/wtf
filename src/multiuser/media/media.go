// Package media removes a device's downloaded-media folder.
//
// It is a leaf package (imports nothing else under multiuser) on purpose:
// both account.Deleter (deleting a whole account) and owners.ScopedDevices
// (unlinking a single number) need to clean up the same folder, and account
// and owners must not import each other.
package media

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// RemoveDevice removes <root>/<deviceID>, the folder a device's downloaded
// media lands in ("<root>/<device id>", e.g. "statics/media/<device id>" in
// production), only when that path is a direct child of root. A symlink
// there is unlinked, not followed, never traversed. An empty root or an
// already-missing folder are both done already (nil).
func RemoveDevice(root, deviceID string) error {
	if root == "" {
		return nil
	}
	cleanRoot := filepath.Clean(root)
	dir := filepath.Join(cleanRoot, deviceID)
	if deviceID == "" || deviceID == "." || deviceID == ".." ||
		strings.ContainsAny(deviceID, `/\`) || filepath.Dir(dir) != cleanRoot || filepath.Base(dir) != deviceID {
		logrus.Warnf("[WTF] media cleanup: refusing media path for device id %q (not directly under the media root)", deviceID)
		return nil
	}
	return os.RemoveAll(dir)
}
