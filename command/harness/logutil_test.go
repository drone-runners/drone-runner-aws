package harness

import (
	"bytes"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

func newTenantLogTestManager(t *testing.T) *drivers.Manager {
	t.Helper()
	base := &config.Google{Image: "projects/p/global/images/base-image"}
	m := &drivers.Manager{}
	require.NoError(t, m.Add(
		drivers.Pool{
			Name: "gcp-mt",
			Spec: base,
			Tenants: []drivers.TenantPool{
				{ID: types.DefaultTenantID, Spec: base},
				{ID: "group-1", Spec: &config.Google{Image: "projects/p/global/images/tenant-image"}},
				{ID: freeAccount, Spec: &config.Google{Image: "projects/p/global/images/free-image"}},
			},
			TenantDrivers: map[string]drivers.Driver{
				types.DefaultTenantID: nil,
				"group-1":             nil,
				freeAccount:           nil,
			},
			AccountToTenant: map[string]string{
				"acctA":     "group-1",
				freeAccount: freeAccount,
			},
		},
		drivers.Pool{Name: "aws-single", Spec: &config.Amazon{AMI: "ami-single"}},
	))
	return m
}

func TestResolveImageForLog_TenantOverrides(t *testing.T) {
	m := newTenantLogTestManager(t)

	tests := []struct {
		name         string
		pool         string
		accountID    string
		imageVersion string
		imageName    string
		want         string
	}{
		{name: "tenant override image", pool: "gcp-mt", accountID: "acctA", want: "tenant-image"},
		{name: "free account tenant override", pool: "gcp-mt", accountID: freeAccount, want: "free-image"},
		{name: "unknown account uses default tenant", pool: "gcp-mt", accountID: "unknown", want: "base-image"},
		{name: "empty account uses default tenant", pool: "gcp-mt", accountID: "", want: "base-image"},
		{name: "single-tenant pool", pool: "aws-single", accountID: "acctA", want: "ami-single"},
		{name: "missing pool", pool: "missing", accountID: "acctA", want: ""},
		{name: "image version wins over tenant spec", pool: "gcp-mt", accountID: "acctA", imageVersion: "v1.2", imageName: "img", want: "v1.2"},
		{name: "image name wins over tenant spec", pool: "gcp-mt", accountID: "acctA", imageName: "img", want: "img"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveImageForLog(m, tt.pool, tt.accountID, tt.imageVersion, tt.imageName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLogRequestedMachine_UsesTenantImage(t *testing.T) {
	m := newTenantLogTestManager(t)

	var buf bytes.Buffer
	l := logrus.New()
	l.Out = &buf
	usePlainFormatter(l)

	platform := &types.Platform{OS: "linux", Arch: "amd64"}
	logRequestedMachine(logrus.NewEntry(l), m, "gcp-mt", "acctA", platform, "large", "", "", "stage-1", true)

	out := buf.String()
	assert.Contains(t, out, "Image Version: tenant-image")
	assert.NotContains(t, out, "base-image")
	assert.Contains(t, out, "Machine Size: large")
	assert.Contains(t, out, "OS: Linux")
	assert.Contains(t, out, "Arch: Amd64")
	assert.Contains(t, out, "Stage Runtime ID: stage-1")
}
