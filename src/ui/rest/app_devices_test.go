package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	domainApp "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/app"
	"github.com/gofiber/fiber/v3"
	"go.mau.fi/whatsmeow/types"
)

// appDevicesStub is a minimal domainApp.IAppUsecase: only FetchDevices
// matters for these tests, every other method is a no-op.
type appDevicesStub struct {
	devices []domainApp.DevicesResponse
}

func (s *appDevicesStub) Login(context.Context, string) (domainApp.LoginResponse, error) {
	return domainApp.LoginResponse{}, nil
}
func (s *appDevicesStub) LoginWithCode(context.Context, string, string) (string, error) {
	return "", nil
}
func (s *appDevicesStub) PasskeyChallenge(context.Context, string) (domainApp.PasskeyChallengeResponse, error) {
	return domainApp.PasskeyChallengeResponse{}, nil
}
func (s *appDevicesStub) PasskeyResponse(context.Context, string, *types.WebAuthnResponse) error {
	return nil
}
func (s *appDevicesStub) PasskeyConfirm(context.Context, string) error { return nil }
func (s *appDevicesStub) Logout(context.Context, string) error        { return nil }
func (s *appDevicesStub) Reconnect(context.Context, string) error     { return nil }
func (s *appDevicesStub) Status(context.Context, string) (bool, bool, error) {
	return false, false, nil
}
func (s *appDevicesStub) FirstDevice(context.Context) (domainApp.DevicesResponse, error) {
	return domainApp.DevicesResponse{}, nil
}
func (s *appDevicesStub) FetchDevices(context.Context) ([]domainApp.DevicesResponse, error) {
	return s.devices, nil
}

type devicesResponseEnvelope struct {
	Results []domainApp.DevicesResponse `json:"results"`
}

func devicesFromResponse(t *testing.T, resp *http.Response) []domainApp.DevicesResponse {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var env devicesResponseEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal body %q: %v", body, err)
	}
	return env.Results
}

// TestDevices_FiltersThroughHookWhenSet proves GET /app/devices narrows its
// response through FilterOwnDevicesFunc when the multi-user layer has wired
// one, rather than always answering with serviceApp.FetchDevices' whole
// unfiltered, server-wide list. Before the fix there was no such hook at
// all, so any authenticated caller who owned one device could see every
// other user's devices too.
func TestDevices_FiltersThroughHookWhenSet(t *testing.T) {
	all := []domainApp.DevicesResponse{
		{Name: "Alice's phone", Device: "d_a", JID: "a@s.whatsapp.net"},
		{Name: "Bob's phone", Device: "d_b", JID: "b@s.whatsapp.net"},
	}
	stub := &appDevicesStub{devices: all}

	app := fiber.New()
	InitRestApp(app, stub)

	t.Cleanup(func() { FilterOwnDevicesFunc = nil })
	FilterOwnDevicesFunc = func(_ context.Context, in []domainApp.DevicesResponse) ([]domainApp.DevicesResponse, error) {
		out := []domainApp.DevicesResponse{}
		for _, d := range in {
			if d.Device == "d_a" {
				out = append(out, d)
			}
		}
		return out, nil
	}

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/app/devices", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	got := devicesFromResponse(t, resp)
	if len(got) != 1 || got[0].Device != "d_a" {
		t.Fatalf("expected only d_a, got %+v", got)
	}
}

// TestDevices_NilHookKeepsUpstreamBehavior proves single-user mode (no hook
// installed) is unchanged: /app/devices still returns the whole list.
func TestDevices_NilHookKeepsUpstreamBehavior(t *testing.T) {
	all := []domainApp.DevicesResponse{
		{Name: "Alice's phone", Device: "d_a", JID: "a@s.whatsapp.net"},
		{Name: "Bob's phone", Device: "d_b", JID: "b@s.whatsapp.net"},
	}
	stub := &appDevicesStub{devices: all}

	app := fiber.New()
	InitRestApp(app, stub)
	FilterOwnDevicesFunc = nil

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/app/devices", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	got := devicesFromResponse(t, resp)
	if len(got) != 2 {
		t.Fatalf("expected both devices without a hook, got %+v", got)
	}
}
