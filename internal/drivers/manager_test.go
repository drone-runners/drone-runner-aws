package drivers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/mocks"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestProvision_NoPoolAvailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.TODO()

	m := &Manager{
		instanceStore: nil,
		poolMap:       map[string]*poolEntry{},
	}

	_, err := m.Provision(ctx, "nonexistentPool", "", nil)

	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProvision_FailedToListInstances(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)

	ctx := context.TODO()
	poolName := "testPool"
	env := setupMockEnvConfig()

	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(nil, errors.New("database error"))

	m := &Manager{
		instanceStore: mockInstanceStore,
		poolMap: map[string]*poolEntry{
			poolName: {Pool: Pool{Name: poolName, MinSize: 1, MaxSize: 3, Driver: nil}},
		},
		runnerName: env.Runner.Name,
	}

	_, err := m.Provision(ctx, poolName, "", nil)

	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to list instances")
}

func TestProvision_NoFreeInstances_CreateNew(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)
	mockDriver := mocks.NewMockDriver(ctrl)

	ctx := context.TODO()
	poolName := "testPool"
	ownerID := "owner1"
	env := setupMockEnvConfig()

	m := &Manager{
		instanceStore: mockInstanceStore,
		poolMap: map[string]*poolEntry{
			poolName: {Pool: Pool{Name: poolName, MinSize: 1, MaxSize: 3, Driver: mockDriver}},
		},
		runnerName: env.Runner.Name,
	}

	emptyInstances := []*types.Instance{} // Adjusted to pointer slice

	// Mocking the calls
	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(emptyInstances, nil)
	mockInstanceStore.EXPECT().Create(ctx, gomock.Any()).Return(nil)
	mockDriver.EXPECT().Create(ctx, gomock.Any()).Return(&types.Instance{ID: "inst1"}, nil)

	// Execute
	inst, err := m.Provision(ctx, poolName, ownerID, env)

	// Assert
	assert.Nil(t, err)
	assert.NotNil(t, inst)
	assert.Equal(t, "inst1", inst.ID)
}

func TestProvision_InstancesAvailable_Use(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)
	mockDriver := mocks.NewMockDriver(ctrl)
	mockLeHttpClient := mocks.NewMockLiteClient(ctrl)
	mockClientFactory := mocks.NewMockClientFactory(ctrl)

	ctx := context.TODO()
	poolName := "testPool"
	ownerID := "owner1"
	env := setupMockEnvConfig()

	m := &Manager{
		instanceStore: mockInstanceStore,
		poolMap: map[string]*poolEntry{
			poolName: {Pool: Pool{Name: poolName, MinSize: 1, MaxSize: 3, Driver: mockDriver}},
		},
		runnerName:    env.Runner.Name,
		clientFactory: mockClientFactory,
	}

	instances := []*types.Instance{
		{
			ID:      "inst1",
			State:   types.StateCreated,
			Address: "127.0.0.1",
			Port:    8080,
			CACert:  []byte("dummyCACert"),
			TLSCert: []byte("dummyTLSCert"),
			TLSKey:  []byte("dummyTLSKey"),
		},
	}

	// Mocking the calls
	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(instances, nil)
	mockInstanceStore.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mockClientFactory.EXPECT().NewClient(
		gomock.Eq(instances[0]),
		gomock.Eq(env.Runner.Name),
		gomock.Eq(instances[0].Port),
		gomock.Eq(env.LiteEngine.EnableMock),
		gomock.Eq(env.LiteEngine.MockStepTimeoutSecs),
	).Return(mockLeHttpClient, nil)
	mockLeHttpClient.EXPECT().RetryHealth(gomock.Any(), gomock.Any()).Return(nil, nil)

	inst, err := m.Provision(ctx, poolName, ownerID, env)

	// Assert
	assert.Nil(t, err)
	assert.NotNil(t, inst)
	assert.Equal(t, "inst1", inst.ID)
	assert.Equal(t, types.StateInUse, inst.State)
	assert.Equal(t, ownerID, inst.OwnerID)
}

func setupMockEnvConfig() *config.EnvConfig {
	c := &config.EnvConfig{}
	c.LiteEngine.Path = "https://github.com/harness/lite-engine/releases/download/v0.5.28/"
	c.LiteEngine.EnableMock = false
	c.LiteEngine.MockStepTimeoutSecs = 120
	c.LiteEngine.HealthCheckTimeout = 5 * time.Second
	c.Runner.Name = "testServer"
	return c
}
