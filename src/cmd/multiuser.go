package cmd

import (
	"os"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/config"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser"
	uimcp "github.com/aldinokemal/go-whatsapp-web-multidevice/ui/mcp"
	"github.com/gofiber/fiber/v3"
	"github.com/spf13/viper"
)

// initAppFunc is initApp; a var so tests can observe whether it runs.
var initAppFunc = initApp

// initAppUnlessSkipped runs initApp unless the command opted out (admin-link).
func initAppUnlessSkipped(args []string) {
	if commandSkipsAppInit(args) {
		return
	}
	initAppFunc()
}

// settingGetenv returns the process environment value for k, else the value
// utils.LoadConfig put into viper from a local .env (which does not populate
// os.Environ). viper keys are case-insensitive.
func settingGetenv(k string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return viper.GetString(k)
}

// loadMultiUserSettings reads WTF_* settings from the environment (Render
// injects them) falling back to the local .env as loaded into viper.
func loadMultiUserSettings() (multiuser.Settings, error) {
	return multiuser.Load(settingGetenv)
}

// applyOrphanAdoptionSetting sets whatsapp.DisableOrphanSlotAdoption before
// LoadExistingDevices ever runs. It must be called from initEnvConfig
// (cobra.OnInitialize's first hook), not from restServer: restServer's own
// multi-user detection (loadMultiUserSettings, config.MultiUserEnabled) only
// runs from inside the "rest" command's Run body, which cobra invokes AFTER
// every OnInitialize hook - including the one that calls initApp, whose
// LoadExistingDevices does the orphan-slot adoption. By the time restServer
// knew multi-user mode was even active, the adoption window had already run
// and closed.
//
// multiuser.Load reads only WTF_MULTIUSER (and, when that is true, a few
// other env vars) - no database connection or other late-available resource
// - so Enabled is safe to read this early even on a path whose fuller
// validation (e.g. SESSION_SECRET) might otherwise fail; Enabled is set
// before any such validation runs and is returned alongside any error.
// restServer's later call to the same function still surfaces that error
// for real (logrus.Fatalln); this call only ever reads the one bool.
func applyOrphanAdoptionSetting() {
	s, _ := loadMultiUserSettings()
	whatsapp.DisableOrphanSlotAdoption = s.Enabled
}

// setupMultiUser is called by restServer when WTF_MULTIUSER=true. It must run
// before any apiGroup registration so the API gate precedes upstream routes.
func setupMultiUser(app *fiber.App, dm *whatsapp.DeviceManager, s multiuser.Settings) (*multiuser.Runtime, error) {
	// multiuser.Apply() already ran at the top of restServer.
	rt, err := multiuser.Setup(s, dm, deviceUsecase, config.PathStorages)
	if err != nil {
		return nil, err
	}
	deviceUsecase = rt.Devices // /devices REST routes now see only the caller's devices
	rt.Install(app, dm, mcpUsecaseDeps())
	// Finish any account deletion left half done, free link slots nobody
	// scanned and, while WTF_RETENTION_HOURS is on, delete message content
	// and media past the window.
	var chats multiuser.ChatData
	if chatStorageRepo != nil {
		chats = chatStorageRepo
	}
	rt.Start(chats)
	return rt, nil
}

// mcpUsecaseDeps are the usecase instances the MCP tools call: the same ones
// the REST handlers hold, so both surfaces share one whatsmeow session.
func mcpUsecaseDeps() uimcp.Deps {
	return uimcp.Deps{
		App:      appUsecase,
		Send:     sendUsecase,
		Schedule: scheduleUsecase,
		Chat:     chatUsecase,
		User:     userUsecase,
		Message:  messageUsecase,
		Group:    groupUsecase,
		// WTF: behind the REST routes upstream's MCP tools lacked.
		Newsletter: newsletterUsecase,
		Call:       callUsecase,
		Uploads:    uploadStore,
	}
}
