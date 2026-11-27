package web

import (
	"errors"
	"strings"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/auth"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/store"
	"github.com/gofiber/fiber/v3"
)

// MountAdminAPI registers POST /admin/api/users: it creates a new WTF user
// and mints that user's own, ordinary (full-scope) API key, in one call,
// handing both back in one response. There is no way to name an existing
// user id here - the key returned is always for the user this same call
// just created - because that was the real escalation hole in an earlier
// design: a second, separate "mint a key for a user id" endpoint would have
// let a leaked provisioning key mint itself a fresh key for anybody,
// including a superadmin's own account.
//
// Reachable by either kind of caller: a provisioning-scoped key, made for
// exactly this, or a superadmin's own ordinary key, since a superadmin can
// already do everything this does anyway. Nobody else - mount this right
// after the API gate (APIKey(), RequireAPI()) and before
// RefuseProvisioningScope, which is what stops a provisioning-scoped key
// from reaching anything registered after it (see runtime.go: that
// ordering is the actual enforcement, not a scope check in this handler).
func MountAdminAPI(app *fiber.App, d Deps) {
	app.Post("/admin/api/users", func(c fiber.Ctx) error {
		caller, _ := userOf(c)
		scope, _ := auth.KeyScopeFrom(c.Context())
		if scope != store.ScopeProvisioning && d.CheckSuperadmin(caller) != nil {
			return forbiddenAdminAPI(c)
		}

		var body struct {
			Phone string `json:"phone"`
		}
		if err := c.Bind().JSON(&body); err != nil || strings.TrimSpace(body.Phone) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"status": fiber.StatusBadRequest, "code": "VALIDATION_ERROR", "message": `send {"phone": "+..."}`,
			})
		}

		phone, _, userID, err := d.AdminAddUser(c.Context(), caller, body.Phone, c.IP())
		var r *Refusal
		switch {
		case errors.As(err, &r):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"status": fiber.StatusBadRequest, "code": "VALIDATION_ERROR", "message": r.Msg,
			})
		case errors.Is(err, ErrNotSuperadmin):
			return forbiddenAdminAPI(c)
		case err != nil:
			return err
		}

		// A user with no key is useless to the caller either way (there is
		// no other way today to mint one for it), so a failure here fails
		// the whole request rather than leaving a keyless user behind.
		raw, _, err := d.Store.CreateAPIKey(c.Context(), userID, "chatter-provisioned")
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"code": "SUCCESS", "message": "User created",
			"results": fiber.Map{"user_id": userID, "phone": phone, "api_key": raw},
		})
	})
}

func forbiddenAdminAPI(c fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"status": fiber.StatusForbidden, "code": "FORBIDDEN",
		"message": "this needs a provisioning key or a superadmin's own key",
	})
}
