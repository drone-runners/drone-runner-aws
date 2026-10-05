package harness

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/drone-runners/drone-runner-aws/metric"
)

func TestRecordNormalCleanupUsageDuration_RecordsWhenUsageStartKnown(t *testing.T) {
	m := &metric.Metrics{VMUsageDurationCount: metric.VMUsageDurationCount(), VMUsageSecondsTotal: metric.VMUsageSecondsTotal()}
	inst := &types.Instance{
		Zone: "us-east1-a", Size: "n1-standard-2", Source: types.InstanceSourcePool,
		OwnerID: "acct-1", Provider: types.DriverType("mock"), Platform: types.Platform{OS: "linux", Arch: "amd64"},
	}
	usageStart := time.Now().Add(-5 * time.Minute).Unix()

	recordNormalCleanupUsageDuration(m, "pool1", inst, usageStart)

	assert.Equal(t, 1, testutil.CollectAndCount(m.VMUsageDurationCount))
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMUsageSecondsTotal))
}

func TestRecordNormalCleanupUsageDuration_SkipsWhenUsageStartUnknown(t *testing.T) {
	m := &metric.Metrics{VMUsageDurationCount: metric.VMUsageDurationCount(), VMUsageSecondsTotal: metric.VMUsageSecondsTotal()}
	inst := &types.Instance{Zone: "us-east1-a", Size: "n1-standard-2", Source: types.InstanceSourcePool}

	assert.NotPanics(t, func() {
		recordNormalCleanupUsageDuration(m, "pool1", inst, 0)
	})

	assert.Equal(t, 0, testutil.CollectAndCount(m.VMUsageDurationCount))
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMUsageSecondsTotal))
}

func TestRecordNormalCleanupUsageDuration_NilMetricsSafe(t *testing.T) {
	inst := &types.Instance{Zone: "us-east1-a", Size: "n1-standard-2", Source: types.InstanceSourcePool}
	assert.NotPanics(t, func() {
		recordNormalCleanupUsageDuration(nil, "pool1", inst, time.Now().Unix())
	})
}
