package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/signin"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// TestPasskeyLoginBeginIsRateLimitedPerIP guards against a DoS on everyone's
// passkey sign-in: BeginLogin reserves an entry in a shared, bounded ceremony
// store (defaultMax = 10000, ceremonyTTL = 5 minutes), and that route is
// public and unauthenticated. Without a per-source limit, one address can
// fill the whole store within the TTL and ErrBusy starts refusing everybody
// else's passkey sign-in.
func TestPasskeyLoginBeginIsRateLimitedPerIP(t *testing.T) {
	e := newEnv(t)
	e.d.PasskeyLoginLimiter = signin.NewLimiter(2, time.Minute)
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(e.d)})
	app.Use(e.d.Auth.Middleware())
	Mount(app, e.d)

	post := func() *http.Response {
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/auth/passkey/login/begin", nil))
		require.NoError(t, err)
		return resp
	}

	for i := 0; i < 2; i++ {
		resp := post()
		require.Equal(t, http.StatusOK, resp.StatusCode, "attempt %d within the limit", i+1)
	}
	resp := post()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "a third attempt from the same address is refused, not reserved")
}
