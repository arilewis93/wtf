package middleware

import (
	"errors"
	"net/url"
	"strings"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/config"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
	pkgError "github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/error"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v3"
)

const DeviceIDHeader = "X-Device-Id"

// WTF: DeviceResolveFunc lets the multi-user layer replace how a request
// picks its device (owner-checked, no "default device" fallback). Nil keeps
// upstream behaviour.
var DeviceResolveFunc func(c fiber.Ctx, deviceID string) (*whatsapp.DeviceInstance, string, error)

// DeviceMiddleware fetches a device instance by header (preferred), path param, or query param
// and injects it into the context. It falls back to the default/only device for single-device mode.
func DeviceMiddleware(dm *whatsapp.DeviceManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Allow non-device-scoped public endpoints (e.g., landing page) to pass through.
		path := strings.TrimSpace(c.Path())
		if path == "/" || path == "" || path == config.AppBasePath || path == config.AppBasePath+"/" {
			return c.Next()
		}

		if dm == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(utils.ResponseData{
				Status:  fiber.StatusServiceUnavailable,
				Code:    "DEVICE_MANAGER_UNAVAILABLE",
				Message: "Device manager is not initialized",
				Results: nil,
			})
		}

		deviceID := strings.TrimSpace(c.Get(DeviceIDHeader))
		// URL-decode the header value to support non-ASCII characters
		if decoded, err := url.QueryUnescape(deviceID); err == nil {
			deviceID = decoded
		}
		if deviceID == "" {
			deviceID = strings.TrimSpace(c.Query("device_id"))
		}

		var (
			instance   *whatsapp.DeviceInstance
			resolvedID string
			err        error
		)
		if DeviceResolveFunc != nil { // WTF
			instance, resolvedID, err = DeviceResolveFunc(c, deviceID)
			if err == nil && instance == nil { // WTF: a nil device must never reach the context
				err = pkgError.ErrDeviceNotFound
				if resolvedID == "" {
					resolvedID = deviceID
				}
			}
		} else {
			instance, resolvedID, err = dm.ResolveDevice(deviceID)
		}
		if err != nil {
			// WTF: a validation-style error (e.g. "more than one of your
			// WhatsApp numbers is named X; rename one...") carries its own
			// real message and status, and must win even though - like a
			// genuine "not found" - it carries a non-empty resolved/typed id
			// (the ambiguous label itself). Tried before the blanket
			// "non-empty id -> 404" fallback below, which would otherwise
			// swallow it into a generic, unhelpful DEVICE_NOT_FOUND.
			// pkgError.ErrDeviceNotFound deliberately does NOT match this: it
			// is a notFoundError, not a ValidationError, so the real "device
			// truly not found" case is unaffected.
			var validation pkgError.ValidationError
			if DeviceResolveFunc != nil && errors.As(err, &validation) {
				return c.Status(validation.StatusCode()).JSON(utils.ResponseData{
					Status: validation.StatusCode(), Code: validation.ErrCode(), Message: validation.Error(),
				})
			}

			// ResolveDevice returns an ID when provided but missing; use it for payload clarity.
			if resolvedID != "" || strings.TrimSpace(deviceID) != "" {
				return c.Status(fiber.StatusNotFound).JSON(utils.ResponseData{
					Status:  fiber.StatusNotFound,
					Code:    "DEVICE_NOT_FOUND",
					Message: "device not found; create a device first from /api/devices or provide a valid X-Device-Id",
					Results: map[string]string{"device_id": resolvedID},
				})
			}

			var generic pkgError.GenericError
			if DeviceResolveFunc != nil && errors.As(err, &generic) { // WTF: e.g. "link a WhatsApp account first"
				return c.Status(generic.StatusCode()).JSON(utils.ResponseData{
					Status: generic.StatusCode(), Code: generic.ErrCode(), Message: generic.Error(),
				})
			}

			return c.Status(fiber.StatusBadRequest).JSON(utils.ResponseData{
				Status:  fiber.StatusBadRequest,
				Code:    "DEVICE_ID_REQUIRED",
				Message: "device_id is required via X-Device-Id header or device_id query",
				Results: nil,
			})
		}

		c.Locals("device_id", resolvedID)
		c.Locals("device", instance)
		c.SetContext(whatsapp.ContextWithDevice(c.Context(), instance))
		return c.Next()
	}
}
