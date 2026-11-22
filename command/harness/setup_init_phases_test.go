package harness

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestTagAndConnectInstance_HarnessAccountIDMetadata(t *testing.T) {
	cases := []struct {
		name      string
		provider  types.DriverType
		accountID string
		want      map[string]string
	}{
		{"gcp keeps the case of the account id", types.Google, "Acc-AbC123",
			map[string]string{"team": "ci", harnessAccountIDMetadataKey: "Acc-AbC123"}},
		{"gcp without an account id", types.Google, "", map[string]string{"team": "ci"}},
		{"other providers are untouched", types.Amazon, "Acc-AbC123", map[string]string{"team": "ci"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			poolManager := &fakeIManager{}
			tags := map[string]string{"team": "ci"}

			_, err := tagAndConnectInstance(context.Background(), poolManager, &types.Instance{Provider: tc.provider},
				"pool1", "stage1", tc.accountID, tags, true, 1)

			require.NoError(t, err)
			assert.Equal(t, tc.want, poolManager.instanceTags)
			assert.Equal(t, map[string]string{"team": "ci"}, tags, "the caller's tags are not modified")
		})
	}
}

func TestHandleSetup_FreeOwnerTagsGCPInstanceWithRealAccountID(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Provider: types.Google, Zone: "us-east1-b", Size: "n1-standard-2"}
	poolManager := &fakeIManager{
		provisionFunc: func(context.Context) (*types.Instance, *types.CapacityReservation, bool, string, error) {
			return inst, nil, true, "default", nil
		},
	}
	req := newTestSetupRequest()
	req.Context = Context{AccountID: "Acc-AbC123", IsFreeAccount: true}

	_, _, _, _, _ = handleSetup(context.Background(), discardLogr(), discardLogr(), req, //nolint:dogsled
		"runner", true, 1, poolManager, "pool1", types.FreeAccountID, nil, "", newTestInitMetrics(), nil)

	assert.Equal(t, []string{types.FreeAccountID}, poolManager.provisionOwners, "routing owner stays free")
	assert.Equal(t, "Acc-AbC123", poolManager.instanceTags[harnessAccountIDMetadataKey])
}
