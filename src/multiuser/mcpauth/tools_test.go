package mcpauth

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMyDevicesListsOnlyMine(t *testing.T) {
	e := newMCPEnv(t)
	require.NoError(t, e.st.SetOwner(context.Background(), "d_spare", e.alice.ID))
	r := e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil)))
	isErr, text := toolOutcome(t, r)
	require.False(t, isErr, text)
	res := r.body["result"].(map[string]any)
	b, _ := json.Marshal(res["structuredContent"])
	var out struct {
		Total int `json:"total"`
		Items []struct {
			DeviceID string `json:"device_id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(b, &out))
	var ids []string
	for _, it := range out.Items {
		ids = append(ids, it.DeviceID)
	}
	assert.ElementsMatch(t, []string{"d_alice", "d_spare"}, ids)
	assert.Equal(t, len(out.Items), out.Total)
	assert.NotContains(t, r.raw, "d_bob")
}

// Behaviour: a number's own name (its label) comes back with it.
func TestMyDevicesCarriesTheLabel(t *testing.T) {
	e := newMCPEnv(t)
	require.NoError(t, e.st.SetLabel(context.Background(), e.alice.ID, "d_alice", "Work phone"))
	r := e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil)))
	isErr, text := toolOutcome(t, r)
	require.False(t, isErr, text)
	b, _ := json.Marshal(r.body["result"].(map[string]any)["structuredContent"])
	var out struct {
		Items []struct {
			DeviceID string `json:"device_id"`
			Label    string `json:"label"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(b, &out))
	require.Len(t, out.Items, 1)
	assert.Equal(t, "Work phone", out.Items[0].Label)
}

// Behaviour: the text summary, not only the structured content, says what a
// caller needs to resolve a device_id (T: wtf_my_devices summary fix).

func TestMyDevicesSummaryWithNoDevices(t *testing.T) {
	e := newMCPEnv(t)
	require.NoError(t, e.st.RemoveOwner(context.Background(), "d_alice"))
	_, text := toolOutcome(t, e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil))))
	assert.Equal(t, "No WhatsApp numbers linked yet.", text)
}

func TestMyDevicesSummaryWithOneLabelledDevice(t *testing.T) {
	e := newMCPEnv(t)
	require.NoError(t, e.st.SetLabel(context.Background(), e.alice.ID, "d_alice", "Work phone"))
	_, text := toolOutcome(t, e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil))))
	assert.Equal(t, "1 WhatsApp number linked: Work phone", text)
}

// With exactly one number nothing needs to disambiguate it, so the
// device_id is left out of the sentence - the other WhatsApp tools already
// default to it with none given.
func TestMyDevicesSummaryWithOneUnlabelledDeviceOmitsTheID(t *testing.T) {
	e := newMCPEnv(t)
	_, text := toolOutcome(t, e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil))))
	assert.Equal(t, "1 WhatsApp number linked: unnamed", text)
	assert.NotContains(t, text, "d_alice")
}

func TestMyDevicesSummaryWithTwoLabelledDevicesNamesEveryOneWithItsID(t *testing.T) {
	e := newMCPEnv(t)
	ctx := context.Background()
	require.NoError(t, e.st.SetOwner(ctx, "d_spare", e.alice.ID))
	require.NoError(t, e.st.SetLabel(ctx, e.alice.ID, "d_alice", "Work phone"))
	require.NoError(t, e.st.SetLabel(ctx, e.alice.ID, "d_spare", "Personal"))
	_, text := toolOutcome(t, e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil))))
	assert.Equal(t, "2 WhatsApp numbers linked: Work phone (d_alice), Personal (d_spare)", text)
}

// A mix of labelled and unlabelled numbers: every one of them still carries
// its device_id, labelled or not, because that id is the literal thing a
// caller needs to disambiguate and resolve_device cannot invent it.
func TestMyDevicesSummaryWithAMixOfLabelledAndUnlabelledNamesEveryID(t *testing.T) {
	e := newMCPEnv(t)
	ctx := context.Background()
	require.NoError(t, e.st.SetOwner(ctx, "d_spare", e.alice.ID))
	require.NoError(t, e.st.SetLabel(ctx, e.alice.ID, "d_alice", "Work phone"))
	_, text := toolOutcome(t, e.rpc(mcpRequest(e.aliceAT, "", toolCall(MyDevicesTool, nil))))
	assert.Equal(t, "2 WhatsApp numbers linked: Work phone (d_alice), unnamed (d_spare)", text)
	assert.Contains(t, text, "d_alice")
	assert.Contains(t, text, "d_spare")
}
