package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/owners"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

func TestDeviceResolveFuncOverridesManager(t *testing.T) {
	dm := whatsapp.NewDeviceManager(nil, nil, nil)
	_, err := dm.CreateDevice(t.Context(), "upstream-default")
	require.NoError(t, err)

	saved := DeviceResolveFunc
	t.Cleanup(func() { DeviceResolveFunc = saved })
	var asked string
	DeviceResolveFunc = func(_ fiber.Ctx, id string) (*whatsapp.DeviceInstance, string, error) {
		asked = id
		return nil, id, pkgError.ErrDeviceNotFound
	}

	app := fiber.New()
	app.Get("/x", DeviceMiddleware(dm), func(c fiber.Ctx) error { return c.SendString("reached") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(DeviceIDHeader, "someone-elses")
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "someone-elses", asked)

	// With no header the hook decides too: upstream's "only device" fallback is never used.
	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.NoError(t, err)
	require.NotEqual(t, http.StatusOK, resp.StatusCode)
}

// The multi-user resolver's ownership errors must reach the caller with their own
// message, not the generic DEVICE_ID_REQUIRED text.
func TestDeviceResolveFuncPassesOwnershipErrorMessages(t *testing.T) {
	dm := whatsapp.NewDeviceManager(nil, nil, nil)
	saved := DeviceResolveFunc
	t.Cleanup(func() { DeviceResolveFunc = saved })

	for _, ownErr := range []error{owners.ErrNoDeviceLinked, owners.ErrDeviceIDRequired} {
		DeviceResolveFunc = func(fiber.Ctx, string) (*whatsapp.DeviceInstance, string, error) {
			return nil, "", ownErr
		}
		app := fiber.New()
		app.Get("/x", DeviceMiddleware(dm), func(c fiber.Ctx) error { return c.SendString("reached") })

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var got utils.ResponseData
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, ownErr.Error(), got.Message)
		require.Equal(t, "VALIDATION_ERROR", got.Code)
	}
}

// A ValidationError from the resolver (e.g. two of the user's own devices
// sharing a label) must reach the caller with its own message and status,
// not the generic 404 DEVICE_NOT_FOUND - even though, like a real "not
// found", it carries a non-empty resolved/typed id (the ambiguous label
// itself). Regression for the "ambiguous label" fix: the blanket
// "resolvedID or deviceID non-empty -> 404" branch used to run first and
// swallow this.
func TestDeviceResolveFuncPassesAmbiguousLabelMessage(t *testing.T) {
	dm := whatsapp.NewDeviceManager(nil, nil, nil)
	saved := DeviceResolveFunc
	t.Cleanup(func() { DeviceResolveFunc = saved })
	ambiguous := pkgError.ValidationError(`more than one of your WhatsApp numbers is named "office"; rename one so the names differ, or pass its device_id`)
	DeviceResolveFunc = func(_ fiber.Ctx, id string) (*whatsapp.DeviceInstance, string, error) {
		return nil, id, ambiguous // resolvedID is the typed label, not empty
	}

	app := fiber.New()
	app.Get("/x", DeviceMiddleware(dm), func(c fiber.Ctx) error { return c.SendString("reached") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(DeviceIDHeader, "office")
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "the ValidationError's own status, not a generic 404")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var got utils.ResponseData
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, ambiguous.Error(), got.Message)
	require.Equal(t, "VALIDATION_ERROR", got.Code)
	require.NotEqual(t, "DEVICE_NOT_FOUND", got.Code)
}

// With no hook installed the middleware is upstream's: the only device is the default.
func TestDeviceResolveFuncNilKeepsUpstreamBehaviour(t *testing.T) {
	saved := DeviceResolveFunc
	t.Cleanup(func() { DeviceResolveFunc = saved })
	DeviceResolveFunc = nil

	dm := whatsapp.NewDeviceManager(nil, nil, nil)
	app := fiber.New()
	app.Get("/x", DeviceMiddleware(dm), func(c fiber.Ctx) error { return c.SendString("reached") })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "DEVICE_ID_REQUIRED")

	_, err = dm.CreateDevice(t.Context(), "only")
	require.NoError(t, err)
	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// A hook that returns no device and no error must not let a nil device reach the
// context (usecases would fall back to the global client).
func TestDeviceResolveFuncNilInstanceIsNotFound(t *testing.T) {
	saved := DeviceResolveFunc
	t.Cleanup(func() { DeviceResolveFunc = saved })
	DeviceResolveFunc = func(_ fiber.Ctx, id string) (*whatsapp.DeviceInstance, string, error) {
		return nil, id, nil
	}

	dm := whatsapp.NewDeviceManager(nil, nil, nil)
	app := fiber.New()
	reached := false
	app.Get("/x", DeviceMiddleware(dm), func(c fiber.Ctx) error { reached = true; return c.SendString("reached") })

	for _, header := range []string{"", "d_x"} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if header != "" {
			req.Header.Set(DeviceIDHeader, header)
		}
		resp, err := app.Test(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode, "header %q", header)
		require.False(t, reached)
	}
}
