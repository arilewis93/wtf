package cmd

import (
	"testing"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/stretchr/testify/require"
)

// TestOrphanAdoptionDisabledBeforeInitAppRunsInMultiUserMode is the fix for
// a real startup-ordering bug: DeviceManager.LoadExistingDevices (called
// from initApp, via cobra.OnInitialize's initAppUnlessSkipped hook) can
// silently attach an orphaned WhatsApp session to another user's empty
// slot unless whatsapp.DisableOrphanSlotAdoption is already true by the
// time it runs. In multi-user mode it never was: restServer only detects
// multi-user mode (loadMultiUserSettings, config.MultiUserEnabled) from
// inside the "rest" command's own Run body, which cobra invokes AFTER every
// OnInitialize hook has already run - so the adoption window had already
// opened and closed before anything knew multi-user mode was even active.
//
// This replicates cobra's REAL registered order
// (cobra.OnInitialize(initEnvConfig, func(){ initAppUnlessSkipped(...) }))
// by calling the two hooks in that exact sequence, and swaps initAppFunc to
// observe the flag's value AT THE MOMENT initApp (and so
// LoadExistingDevices) actually runs - proving the ordering, not merely
// that the flag gets set at some point before the test's own assertion.
func TestOrphanAdoptionDisabledBeforeInitAppRunsInMultiUserMode(t *testing.T) {
	t.Setenv("WTF_MULTIUSER", "true")

	savedFlag := whatsapp.DisableOrphanSlotAdoption
	whatsapp.DisableOrphanSlotAdoption = false
	t.Cleanup(func() { whatsapp.DisableOrphanSlotAdoption = savedFlag })

	var sawDisabledWhenInitAppRan bool
	oldInitApp := initAppFunc
	initAppFunc = func() {
		// Stands in for the real initApp body, which calls
		// dm.LoadExistingDevices(ctx) here.
		sawDisabledWhenInitAppRan = whatsapp.DisableOrphanSlotAdoption
	}
	t.Cleanup(func() { initAppFunc = oldInitApp })

	// The exact order cobra.OnInitialize registers in cmd/root.go's init().
	initEnvConfig()
	initAppUnlessSkipped([]string{"rest"})

	require.True(t, sawDisabledWhenInitAppRan,
		"DisableOrphanSlotAdoption must already be true by the time initApp (and so LoadExistingDevices) runs in multi-user mode")
}

// TestOrphanAdoptionStaysOffInSingleUserMode is the control: WTF_MULTIUSER
// unset (or false) must never flip the flag, so single-user startup keeps
// upstream's orphan-adoption behaviour unchanged.
func TestOrphanAdoptionStaysOffInSingleUserMode(t *testing.T) {
	t.Setenv("WTF_MULTIUSER", "false")

	savedFlag := whatsapp.DisableOrphanSlotAdoption
	whatsapp.DisableOrphanSlotAdoption = false
	t.Cleanup(func() { whatsapp.DisableOrphanSlotAdoption = savedFlag })

	initEnvConfig()

	require.False(t, whatsapp.DisableOrphanSlotAdoption)
}
