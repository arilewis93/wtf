package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domainDevice "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/device"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/owners"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/multiuser/store"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// withAdminAPIRoutes mounts the pages plus the admin API and the label API
// in the order runtime.go's Install wires them for Task 2: the API-key/
// cookie gate, then same-origin/CSRF (a key passes through both untouched;
// a cookie session does not - this is what stops a cross-site browser POST
// from a signed-in superadmin's own session), then MountAdminAPI
// (reachable by a provisioning-scoped key or a superadmin's own key, by
// either auth method), then RefuseProvisioningScope, then every route
// after it (MountDeviceLabelAPI stands in for "the upstream REST API"
// here).
func withAdminAPIRoutes(t *testing.T, stub *devStub) *env {
	t.Helper()
	e := newEnv(t)
	e.d.Devices = owners.NewScopedDevices(stub, e.st)
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(e.d)})
	app.Use(e.d.Auth.Middleware())
	Mount(app, e.d)
	app.Use(e.d.Auth.APIKey(), e.d.Auth.RequireAPI())
	app.Use(e.d.Auth.RequireSameOriginAPI(), e.d.Auth.VerifyCSRF())
	MountAdminAPI(app, e.d)
	app.Use(e.d.Auth.RefuseProvisioningScope())
	MountDeviceLabelAPI(app, e.d)
	e.app = app
	return e
}

// postAdminAPIUsersAsCookie posts to /admin/api/users authenticated by a
// cookie session (never a key), optionally carrying an X-CSRF-Token header -
// a JSON body has no form field for VerifyCSRF's bodyCSRF to read, so the
// header is the only way a cookie-authenticated caller can pass it here.
func postAdminAPIUsersAsCookie(t *testing.T, e *env, ck *http.Cookie, csrf, phone string) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"phone": phone})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(ck)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return e.do(t, req)
}

func postAdminAPIUsers(t *testing.T, e *env, bearer, phone string) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"phone": phone})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return e.do(t, req)
}

// Security: an ordinary (non-superadmin, full-scope) user's own key is
// refused - this is still an admin-only action for anyone without a
// provisioning-scoped key or superadmin status.
func TestCreatingAUserNeedsAProvisioningKeyOrSuperadmin(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	_, plainID := signedIn(t, e, "+27825550101")
	key, _, err := e.st.CreateAPIKey(context.Background(), plainID, "script")
	require.NoError(t, err)

	resp, body := postAdminAPIUsers(t, e, key, "+15555550123")
	require.Equal(t, http.StatusForbidden, resp.StatusCode, body)
	_, err = e.st.UserByPhone(context.Background(), "+15555550123")
	require.Error(t, err, "no account created")
}

// Behaviour: a provisioning-scoped key creates a user AND gets that user's
// own key back, in one response - never a second call, never a way to name
// an existing user id.
func TestCreatingAUserWithAProvisioningKeyReturnsTheNewUserAndItsOwnKey(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	admin, adminID := signedIn(t, e, "+27825550100")
	provKey, _, err := e.st.CreateProvisioningAPIKey(context.Background(), adminID, "chatter")
	require.NoError(t, err)
	_ = admin

	resp, body := postAdminAPIUsers(t, e, provKey, "+1 (555) 555-0123")
	require.Equal(t, http.StatusCreated, resp.StatusCode, body)

	var parsed struct {
		Results struct {
			UserID string `json:"user_id"`
			Phone  string `json:"phone"`
			APIKey string `json:"api_key"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	require.NotEmpty(t, parsed.Results.UserID)
	require.Equal(t, "+15555550123", parsed.Results.Phone)
	require.True(t, strings.HasPrefix(parsed.Results.APIKey, store.APIKeyPrefix))

	u, err := e.st.UserByID(context.Background(), parsed.Results.UserID)
	require.NoError(t, err)
	require.Equal(t, "+15555550123", u.Phone)

	newUser, _, scope, err := e.st.UserByAPIKey(context.Background(), parsed.Results.APIKey)
	require.NoError(t, err)
	require.Equal(t, parsed.Results.UserID, newUser.ID)
	require.Equal(t, store.ScopeFull, scope, "the new user's own key is ordinary, never scoped")
}

// Behaviour: a superadmin's own ordinary (full-scope) key still works here
// too - a superadmin can already do everything.
func TestASuperadminsOwnOrdinaryKeyCanAlsoCreateAUser(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	_, adminID := signedIn(t, e, "+27825550100")
	key, _, err := e.st.CreateAPIKey(context.Background(), adminID, "admin's own")
	require.NoError(t, err)

	resp, body := postAdminAPIUsers(t, e, key, "+15555550124")
	require.Equal(t, http.StatusCreated, resp.StatusCode, body)
	_, err = e.st.UserByPhone(context.Background(), "+15555550124")
	require.NoError(t, err)
}

// Security (Review Focus): the SAME provisioning-scoped key, used against an
// existing, unrelated route, is refused with the same restriction, not let
// through - proving the restriction is structural (registration order),
// not a check this one new handler remembers to make.
func TestAProvisioningScopedKeyIsRefusedOnAnyOtherRoute(t *testing.T) {
	stub := &devStub{devices: []domainDevice.Device{{ID: "d_mine"}}}
	e := withAdminAPIRoutes(t, stub)
	_, adminID := signedIn(t, e, "+27825550100")
	require.NoError(t, e.st.SetOwner(context.Background(), "d_mine", adminID))
	provKey, _, err := e.st.CreateProvisioningAPIKey(context.Background(), adminID, "chatter")
	require.NoError(t, err)

	resp, body := putLabelAPI(t, e, provKey, "d_mine", "Shop")
	require.Equal(t, http.StatusForbidden, resp.StatusCode, body)
	labels, err := e.st.LabelsOf(context.Background(), adminID)
	require.NoError(t, err)
	require.Equal(t, "", labels["d_mine"], "nothing changed")
}

// Behaviour: unchanged from AdminAddUser's own existing behaviour - a
// duplicate phone refuses in plain words, not a generic 500.
func TestCreatingAUserForAnAlreadyRegisteredPhoneRefusesInPlainWords(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	_, adminID := signedIn(t, e, "+27825550100")
	provKey, _, err := e.st.CreateProvisioningAPIKey(context.Background(), adminID, "chatter")
	require.NoError(t, err)
	_, _ = signedIn(t, e, "+27825550101")

	resp, body := postAdminAPIUsers(t, e, provKey, "+27 82 555 0101")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
	require.Contains(t, body, "already has an account")
}

// Behaviour: a missing/empty phone is a 400, not a panic or a 500.
func TestCreatingAUserNeedsAPhone(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	_, adminID := signedIn(t, e, "+27825550100")
	provKey, _, err := e.st.CreateProvisioningAPIKey(context.Background(), adminID, "chatter")
	require.NoError(t, err)

	resp, _ := postAdminAPIUsers(t, e, provKey, "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// Security: no bearer at all is refused like any other unauthenticated
// request to the API, not specially accepted.
func TestCreatingAUserNeedsAuthentication(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	resp, _ := postAdminAPIUsers(t, e, "", "+15555550199")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// Security: a superadmin's own COOKIE session (not an API key) still needs
// the CSRF token here, same as every other state-changing route - without
// it, a cross-site page the superadmin merely has open in another tab
// could silently create a WTF user on their behalf. This is what the
// same-origin/CSRF gate running BEFORE MountAdminAPI (not after) buys:
// without it this request would have gone straight through.
func TestCreatingAUserAsACookieSessionNeedsCSRF(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	admin, _ := signedIn(t, e, "+27825550100")

	resp, _ := postAdminAPIUsersAsCookie(t, e, admin, "", "+15555550198")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_, err := e.st.UserByPhone(context.Background(), "+15555550198")
	require.Error(t, err, "no account created without a valid CSRF token")

	resp, body := postAdminAPIUsersAsCookie(t, e, admin, csrfFor(t, e, admin), "+15555550198")
	require.Equal(t, http.StatusCreated, resp.StatusCode, body, "the same request with a real token works")
	_, err = e.st.UserByPhone(context.Background(), "+15555550198")
	require.NoError(t, err)
}

// Security: a simulated cross-site request (Sec-Fetch-Site: cross-site) is
// refused by RequireSameOriginAPI before VerifyCSRF is even reached, token
// or not - the same protection every other cookie-authenticated API route
// gets, now covering this one too.
func TestCreatingAUserAsACookieSessionRefusesCrossSite(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	admin, _ := signedIn(t, e, "+27825550100")
	csrf := csrfFor(t, e, admin)

	b, _ := json.Marshal(map[string]string{"phone": "+15555550197"})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(admin)
	resp, _ := e.do(t, req)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_, err := e.st.UserByPhone(context.Background(), "+15555550197")
	require.Error(t, err)
}

// Behaviour: a provisioning-scoped key still works here with no CSRF
// token or Sec-Fetch-Site header at all, exactly as before the reorder -
// moving the same-origin/CSRF gate earlier costs a key-authenticated
// caller nothing (APIKey() clears the session id the CSRF checks key off).
func TestProvisioningKeyStillNeedsNoCSRFAfterTheReorder(t *testing.T) {
	e := withAdminAPIRoutes(t, &devStub{})
	_, adminID := signedIn(t, e, "+27825550100")
	provKey, _, err := e.st.CreateProvisioningAPIKey(context.Background(), adminID, "chatter")
	require.NoError(t, err)

	resp, body := postAdminAPIUsers(t, e, provKey, "+15555550196")
	require.Equal(t, http.StatusCreated, resp.StatusCode, body)
}
