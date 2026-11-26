package owners

import (
	"context"
	"testing"

	domainApp "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/app"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/auth"
	"github.com/stretchr/testify/require"
)

// TestFilterOwnDevicesIsolation is the fix for GET /app/devices leaking
// every user's devices in multi-user mode: two users each own a device, and
// each must only see their own when the raw, unfiltered FetchDevices list
// (as serviceApp.FetchDevices/ /app/devices produces it) is narrowed by
// FilterOwnDevices, the same ownership check ListDevices already applies.
func TestFilterOwnDevicesIsolation(t *testing.T) {
	st, _, a, b := fixture(t)
	all := []domainApp.DevicesResponse{
		{Name: "A1", Device: "d_a1", JID: "a1@s.whatsapp.net"},
		{Name: "A2", Device: "d_a2", JID: "a2@s.whatsapp.net"},
		{Name: "B1", Device: "d_b1", JID: "b1@s.whatsapp.net"},
		{Name: "Orphan", Device: "d_orphan", JID: "o@s.whatsapp.net"},
	}

	ctxA := auth.WithUser(context.Background(), a)
	outA, err := FilterOwnDevices(ctxA, st, all)
	require.NoError(t, err)
	require.Len(t, outA, 2)
	gotA := []string{outA[0].Device, outA[1].Device}
	require.ElementsMatch(t, []string{"d_a1", "d_a2"}, gotA)

	ctxB := auth.WithUser(context.Background(), b)
	outB, err := FilterOwnDevices(ctxB, st, all)
	require.NoError(t, err)
	require.Len(t, outB, 1)
	require.Equal(t, "d_b1", outB[0].Device)

	// No authenticated caller: nothing is returned, never the whole
	// unscoped list (fail closed, same as ScopedDevices.ListDevices).
	outAnon, err := FilterOwnDevices(context.Background(), st, all)
	require.NoError(t, err)
	require.Empty(t, outAnon)
}
