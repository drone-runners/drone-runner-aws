package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestIsStartStepDeadlineExceeded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "wrapped deadline exceeded", err: errors.New("failed: context deadline exceeded"), want: true},
		{name: "other error", err: errors.New("connection refused"), want: false},
		{name: "nil error", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isStartStepDeadlineExceeded(tt.err))
		})
	}
}

func TestPreserveInstanceForDebug(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	inst := &types.Instance{ID: "inst-1", State: types.StateInUse}
	var updated *types.Instance

	pm := &testPoolManager{
		findFunc: func(_ context.Context, id string) (*types.Instance, error) {
			assert.Equal(t, "inst-1", id)
			return &types.Instance{ID: id, State: types.StateInUse}, nil
		},
		updateFunc: func(_ context.Context, instance *types.Instance) error {
			updated = instance
			return nil
		},
	}

	err := preserveInstanceForDebug(ctx, inst, pm, logrusEntry())
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, types.StatePreserved, updated.State)
	assert.NotZero(t, updated.Updated)
}

func TestPreserveInstanceForDebug_MissingInstanceID(t *testing.T) {
	t.Parallel()

	err := preserveInstanceForDebug(context.Background(), &types.Instance{}, &testPoolManager{}, logrusEntry())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance id is required")
}

func TestHandleDestroy_PreservedInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stageID := "stage-preserved"
	poolName := "pool-1"

	var destroyCalled, destroyCapacityCalled, instanceDeleted bool
	instStore := &trackingInstanceStore{
		deleteFunc: func(_ context.Context, id string) error {
			assert.Equal(t, "inst-preserved", id)
			instanceDeleted = true
			return nil
		},
	}
	pm := &testPoolManager{
		instStore: instStore,
		getInstanceByStageIDFunc: func(_ context.Context, pool, stage string) (*types.Instance, error) {
			assert.Equal(t, poolName, pool)
			assert.Equal(t, stageID, stage)
			return &types.Instance{
				ID:       "inst-preserved",
				Name:     "vm-preserved",
				Address:  "10.0.0.1",
				Port:     9079,
				State:    types.StatePreserved,
				Platform: types.Platform{OS: "linux", Arch: "amd64"},
			}, nil
		},
		updateFunc: func(_ context.Context, instance *types.Instance) error {
			assert.Equal(t, types.StateTerminating, instance.State)
			return nil
		},
		destroyFunc: func(context.Context, string, string, *types.Instance, *storage.CleanupType) error {
			destroyCalled = true
			return nil
		},
		destroyCapacityFunc: func(context.Context, *types.CapacityReservation) error {
			destroyCapacityCalled = true
			return nil
		},
	}

	crs := &testCapacityReservationStore{
		findFunc: func(context.Context, string) (*types.CapacityReservation, error) {
			return &types.CapacityReservation{ReservationID: "cap-1", StageID: stageID}, nil
		},
	}
	sos := &testStageOwnerStore{
		deleteFunc: func(context.Context, string) error { return nil },
	}

	req := &VMCleanupRequest{
		StageRuntimeID: stageID,
		InstanceInfo:   common.InstanceInfo{PoolName: poolName},
	}

	_, err := handleDestroy(ctx, req, sos, crs, true, 1, pm, &metric.Metrics{}, 0, logrusEntry())
	require.NoError(t, err)
	assert.False(t, destroyCalled, "cloud destroy must be skipped for preserved instances")
	assert.False(t, destroyCapacityCalled, "capacity destroy must be skipped for preserved instances")
	assert.True(t, instanceDeleted, "instance row must be removed from store")
}

func TestHandleDestroy_NormalInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stageID := "stage-normal"
	poolName := "pool-1"

	var destroyCalled, destroyCapacityCalled, instanceDeleted bool
	pm := &testPoolManager{
		instStore: &trackingInstanceStore{
			deleteFunc: func(context.Context, string) error {
				instanceDeleted = true
				return nil
			},
		},
		updateFunc: func(context.Context, *types.Instance) error { return nil },
		destroyFunc: func(context.Context, string, string, *types.Instance, *storage.CleanupType) error {
			destroyCalled = true
			return nil
		},
		destroyCapacityFunc: func(context.Context, *types.CapacityReservation) error {
			destroyCapacityCalled = true
			return nil
		},
	}
	crs := &testCapacityReservationStore{
		findFunc: func(context.Context, string) (*types.CapacityReservation, error) {
			return &types.CapacityReservation{ReservationID: "cap-1"}, nil
		},
	}
	sos := &testStageOwnerStore{
		deleteFunc: func(context.Context, string) error { return nil },
	}

	info := preservedInstanceInfo(poolName)
	info.ID = "inst-normal"
	instFromInfo := common.BuildInstanceFromRequest(info)
	instFromInfo.State = types.StateInUse
	pm.getInstanceByStageIDFunc = func(context.Context, string, string) (*types.Instance, error) {
		return instFromInfo, nil
	}
	req := &VMCleanupRequest{StageRuntimeID: stageID, InstanceInfo: common.InstanceInfo{PoolName: poolName}}

	inst, err := handleDestroy(ctx, req, sos, crs, true, 1, pm, &metric.Metrics{}, 0, logrusEntry())
	require.NoError(t, err)
	require.NotNil(t, inst)
	assert.True(t, destroyCalled, "cloud destroy must run for non-preserved instances")
	assert.True(t, destroyCapacityCalled, "capacity destroy must run for non-preserved instances")
	assert.False(t, instanceDeleted, "poolManager.Destroy owns store delete for normal path")
}

func preservedInstanceInfo(poolName string) common.InstanceInfo {
	return common.InstanceInfo{
		ID:                "inst-preserved",
		Name:              "vm-preserved",
		IPAddress:         "10.0.0.1",
		Port:              9079,
		OS:                "linux",
		Arch:              "amd64",
		Provider:          string(types.Google),
		PoolName:          poolName,
		Zone:              "us-central1-a",
		StorageIdentifier: "disk-1",
		CAKey:             []byte("ca-key"),
		CACert:            []byte("ca-cert"),
		TLSKey:            []byte("tls-key"),
		TLSCert:           []byte("tls-cert"),
	}
}

// --- test doubles ---

type trackingInstanceStore struct {
	deleteFunc func(context.Context, string) error
}

func (s *trackingInstanceStore) Find(context.Context, string) (*types.Instance, error) {
	return nil, errors.New("not implemented")
}
func (s *trackingInstanceStore) List(context.Context, string, *types.QueryParams) ([]*types.Instance, error) {
	return nil, nil
}
func (s *trackingInstanceStore) Create(context.Context, *types.Instance) error { return nil }
func (s *trackingInstanceStore) Delete(ctx context.Context, id string) error {
	if s.deleteFunc != nil {
		return s.deleteFunc(ctx, id)
	}
	return nil
}
func (s *trackingInstanceStore) Update(context.Context, *types.Instance) error { return nil }
func (s *trackingInstanceStore) Purge(context.Context) error                   { return nil }
func (s *trackingInstanceStore) DeleteAndReturn(context.Context, string, ...any) ([]*types.Instance, error) {
	return nil, nil
}
func (s *trackingInstanceStore) FindAndClaim(context.Context, *types.QueryParams, types.InstanceState, []types.InstanceState, bool) (*types.Instance, error) {
	return nil, errors.New("not implemented")
}
func (s *trackingInstanceStore) CountGroupedInstances(context.Context, types.InstanceState) ([]types.InstanceCount, error) {
	return nil, nil
}

type testStageOwnerStore struct {
	deleteFunc func(context.Context, string) error
}

func (s *testStageOwnerStore) Find(context.Context, string) (*types.StageOwner, error) {
	return nil, errors.New("not found")
}
func (s *testStageOwnerStore) Create(context.Context, *types.StageOwner) error { return nil }
func (s *testStageOwnerStore) Delete(ctx context.Context, id string) error {
	if s.deleteFunc != nil {
		return s.deleteFunc(ctx, id)
	}
	return nil
}

type testCapacityReservationStore struct {
	findFunc func(context.Context, string) (*types.CapacityReservation, error)
}

func (s *testCapacityReservationStore) Find(ctx context.Context, id string) (*types.CapacityReservation, error) {
	if s.findFunc != nil {
		return s.findFunc(ctx, id)
	}
	return nil, errors.New("not found")
}
func (s *testCapacityReservationStore) Create(context.Context, *types.CapacityReservation) error {
	return nil
}
func (s *testCapacityReservationStore) Delete(context.Context, string) error { return nil }
func (s *testCapacityReservationStore) List(context.Context, *types.CapacityReservationQueryParams, []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
	return nil, nil
}

//nolint:lll // interface signature
func (s *testCapacityReservationStore) FindAndClaim(context.Context, *types.CapacityReservationQueryParams, types.CapacityReservationState, []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
	return nil, nil
}

type testPoolManager struct {
	instStore                store.InstanceStore
	findFunc                 func(context.Context, string) (*types.Instance, error)
	getInstanceByStageIDFunc func(context.Context, string, string) (*types.Instance, error)
	updateFunc               func(context.Context, *types.Instance) error
	destroyFunc              func(context.Context, string, string, *types.Instance, *storage.CleanupType) error
	destroyCapacityFunc      func(context.Context, *types.CapacityReservation) error
}

func logrusEntry() *logrus.Entry {
	return logrus.NewEntry(logrus.New())
}

func (m *testPoolManager) GetInstanceStore() store.InstanceStore {
	if m.instStore != nil {
		return m.instStore
	}
	return &trackingInstanceStore{}
}

func (m *testPoolManager) Find(ctx context.Context, id string) (*types.Instance, error) {
	if m.findFunc != nil {
		return m.findFunc(ctx, id)
	}
	return nil, errors.New("not found")
}

func (m *testPoolManager) Update(ctx context.Context, inst *types.Instance) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, inst)
	}
	return nil
}

func (m *testPoolManager) Destroy(ctx context.Context, poolName, instanceID string, inst *types.Instance, cleanup *storage.CleanupType) error {
	if m.destroyFunc != nil {
		return m.destroyFunc(ctx, poolName, instanceID, inst, cleanup)
	}
	return nil
}

func (m *testPoolManager) DestroyCapacity(ctx context.Context, reservation *types.CapacityReservation) error {
	if m.destroyCapacityFunc != nil {
		return m.destroyCapacityFunc(ctx, reservation)
	}
	return nil
}

func (m *testPoolManager) GetTLSServerName() string            { return "localhost" }
func (m *testPoolManager) IsDistributed() bool                 { return false }
func (m *testPoolManager) IsHosted() bool                      { return false }
func (m *testPoolManager) GetSetupTimeout() time.Duration      { return time.Minute }
func (m *testPoolManager) GetStartStepTimeout() time.Duration  { return time.Minute }
func (m *testPoolManager) GetRunnerConfig() types.RunnerConfig { return types.RunnerConfig{} }
func (m *testPoolManager) GetStageOwnerStore() store.StageOwnerStore {
	return &testStageOwnerStore{}
}
func (m *testPoolManager) GetCapacityReservationStore() store.CapacityReservationStore {
	return &testCapacityReservationStore{}
}

//nolint:lll,gocritic // interface signature
func (m *testPoolManager) Provision(context.Context, string, string, string, *types.ProvisionParams, *types.QueryParams, *types.GitspaceAgentConfig, *types.StorageConfig, *common.InstanceInfo, int64, bool, *types.CapacityReservation, bool) (inst *types.Instance, capacity *types.CapacityReservation, reused bool, ip string, err error) {
	panic("not implemented")
}
func (m *testPoolManager) GetInstanceByStageID(ctx context.Context, poolName, stage string) (*types.Instance, error) {
	if m.getInstanceByStageIDFunc != nil {
		return m.getInstanceByStageIDFunc(ctx, poolName, stage)
	}
	return nil, errors.New("not found")
}

//nolint:gocritic // interface signature
func (m *testPoolManager) List(context.Context, string, *types.QueryParams) (busy, free, hibernating, provisioning, terminating []*types.Instance, err error) {
	panic("not implemented")
}
func (m *testPoolManager) Exists(string) bool { return true }
func (m *testPoolManager) Add(...drivers.Pool) error {
	return nil
}
func (m *testPoolManager) BuildPools(context.Context) error { return nil }
func (m *testPoolManager) CleanPools(context.Context, bool, bool) error {
	return nil
}

//nolint:gocritic // interface signature
func (m *testPoolManager) Inspect(string) (platform types.Platform, rootDir, driver string) {
	return types.Platform{}, "", ""
}
func (m *testPoolManager) GetPoolSpec(string) (interface{}, error) { return nil, nil }
func (m *testPoolManager) StartInstance(context.Context, string, string, *common.InstanceInfo) (*types.Instance, error) {
	panic("not implemented")
}
func (m *testPoolManager) Suspend(context.Context, string, *types.Instance) error {
	panic("not implemented")
}
func (m *testPoolManager) SetInstanceTags(context.Context, string, *types.Instance, map[string]string) error {
	return nil
}
func (m *testPoolManager) InstanceLogs(context.Context, string, string) (string, error) {
	return "", nil
}
func (m *testPoolManager) ApplyEgressPolicy(context.Context, *types.Instance, []string) ([]string, error) {
	return nil, nil
}
func (m *testPoolManager) PingDriver(context.Context) error { return nil }
func (m *testPoolManager) GetHealthCheckTimeout(string, types.DriverType, bool, bool) time.Duration {
	return time.Second
}
func (m *testPoolManager) GetHealthCheckConnectivityDuration() time.Duration {
	return time.Second
}
func (m *testPoolManager) StartInstancePurger(context.Context, time.Duration, time.Duration, time.Duration, time.Duration) error {
	return nil
}

var _ drivers.IManager = (*testPoolManager)(nil)
