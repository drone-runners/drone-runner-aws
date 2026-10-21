package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestManager_GetPoolSpecForAccount(t *testing.T) {
	base := &config.Google{Image: "projects/p/global/images/base-image"}
	groupSpec := &config.Google{Image: "projects/p/global/images/group-image"}
	acctCSpec := &config.Google{Image: "projects/p/global/images/acctc-image"}
	single := &config.Amazon{AMI: "ami-single"}

	m := &Manager{}
	require.NoError(t, m.Add(
		Pool{
			Name: "gcp-mt",
			Spec: base,
			Tenants: []TenantPool{
				{ID: types.DefaultTenantID, Spec: base},
				{ID: "group-1", Spec: groupSpec},
				{ID: "acctC", Spec: acctCSpec},
			},
			TenantDrivers: map[string]Driver{
				types.DefaultTenantID: nil,
				"group-1":             nil,
				"acctC":               nil,
			},
			AccountToTenant: map[string]string{
				"acctA": "group-1",
				"acctB": "group-1",
				"acctC": "acctC",
			},
		},
		Pool{Name: "aws-single", Spec: single},
		Pool{Name: "no-spec"},
	))

	tests := []struct {
		name        string
		pool        string
		accountID   string
		want        interface{}
		errContains string
	}{
		{name: "group member resolves to group tenant spec", pool: "gcp-mt", accountID: "acctA", want: groupSpec},
		{name: "second group member resolves to same tenant", pool: "gcp-mt", accountID: "acctB", want: groupSpec},
		{name: "account keyed tenant", pool: "gcp-mt", accountID: "acctC", want: acctCSpec},
		{name: "unknown account falls back to default tenant", pool: "gcp-mt", accountID: "unknown", want: base},
		{name: "empty account falls back to default tenant", pool: "gcp-mt", accountID: "", want: base},
		{name: "single-tenant pool ignores account", pool: "aws-single", accountID: "acctA", want: single},
		{name: "missing pool", pool: "missing", accountID: "acctA", errContains: "pool missing not found"},
		{name: "pool without spec", pool: "no-spec", accountID: "acctA", errContains: "does not have a stored spec"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.GetPoolSpecForAccount(tt.pool, tt.accountID)
			if tt.errContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Same(t, tt.want, got)
		})
	}
}

func TestDistributedManager_GetPoolSpecForAccount_Delegates(t *testing.T) {
	base := &config.Google{Image: "base"}
	tenant := &config.Google{Image: "tenant"}

	d := &DistributedManager{}
	require.NoError(t, d.Add(Pool{
		Name:            "gcp-mt",
		Spec:            base,
		Tenants:         []TenantPool{{ID: types.DefaultTenantID, Spec: base}, {ID: "acctA", Spec: tenant}},
		TenantDrivers:   map[string]Driver{types.DefaultTenantID: nil, "acctA": nil},
		AccountToTenant: map[string]string{"acctA": "acctA"},
	}))

	got, err := d.GetPoolSpecForAccount("gcp-mt", "acctA")
	require.NoError(t, err)
	assert.Same(t, tenant, got)
}
