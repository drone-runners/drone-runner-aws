package drivers

import (
	"testing"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestIsEgressPool_TenantAware(t *testing.T) {
	base := &config.Google{EgressControl: false}
	tenantOn := &config.Google{EgressControl: true}

	m := &Manager{
		poolMap: map[string]*poolEntry{
			"gcp-mt": {
				Pool: Pool{
					Name: "gcp-mt",
					Spec: base,
					Tenants: []TenantPool{
						{ID: types.DefaultTenantID, Spec: base},
						{ID: "acctA", Spec: tenantOn},
					},
					TenantDrivers: map[string]Driver{
						types.DefaultTenantID: nil,
						"acctA":               nil,
					},
				},
			},
			"gcp-single": {
				Pool: Pool{
					Name: "gcp-single",
					Spec: &config.Google{EgressControl: true},
				},
			},
		},
	}

	if m.IsEgressPool("gcp-mt", types.DefaultTenantID) {
		t.Errorf("default tenant: want egress=false")
	}
	if m.IsEgressPool("gcp-mt", "") {
		t.Errorf("empty tenant id (default): want egress=false")
	}
	if !m.IsEgressPool("gcp-mt", "acctA") {
		t.Errorf("acctA tenant: want egress=true")
	}
	if !m.IsEgressPool("gcp-single", "ignored") {
		t.Errorf("single-tenant: want egress=true regardless of tenant id")
	}
	if m.IsEgressPool("missing", "acctA") {
		t.Errorf("missing pool: want false")
	}
}
