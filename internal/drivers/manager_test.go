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

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	m := &Manager{
		instanceStore: nil,
		poolMap:       map[string]*poolEntry{},
		globalCtx:     context.Background(), // initialize the global context for this test
	}

	_, err := m.Provision(ctx, "nonexistentPool", "", nil)

	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProvision_FailedToListInstances(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	poolName := "testPool"
	env := setupMockEnvConfig()

	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(nil, errors.New("database error"))

	m := &Manager{
		instanceStore: mockInstanceStore,
		poolMap: map[string]*poolEntry{
			poolName: {Pool: Pool{Name: poolName, MinSize: 1, MaxSize: 3, Driver: nil}},
		},
		runnerName: env.Runner.Name,
		globalCtx:  context.Background(), // initialize the global context for this test
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

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	poolName := "testPool"
	ownerID := "owner1"
	env := setupMockEnvConfig()

	m := &Manager{
		instanceStore: mockInstanceStore,
		poolMap: map[string]*poolEntry{
			poolName: {Pool: Pool{Name: poolName, MinSize: 1, MaxSize: 3, Driver: mockDriver}},
		},
		runnerName: env.Runner.Name,
		globalCtx:  context.Background(), // initialize the global context for this test
	}

	emptyInstances := []*types.Instance{} // Adjusted to pointer slice

	// Mocking the calls
	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(emptyInstances, nil)
	mockInstanceStore.EXPECT().Create(ctx, gomock.Any()).Return(nil)
	mockDriver.EXPECT().Create(ctx, gomock.Any()).Return(&types.Instance{ID: "inst1"}, nil)

	// Execute
	inst, err := m.Provision(ctx, poolName, ownerID, env)
	m.wg.Wait() // Wait for all goroutines to finish

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

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

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
		globalCtx:     context.Background(), // initialize the global context for this test
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

	instance := &types.Instance{
		ID:    "inst2",
		State: types.StateCreated,
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
	mockDriver.EXPECT().Create(context.Background(), gomock.Any()).Return(instance, nil)
	mockInstanceStore.EXPECT().Create(context.Background(), gomock.Any()).Return(nil)
	mockDriver.EXPECT().CanHibernate().Return(false).AnyTimes()

	inst, err := m.Provision(ctx, poolName, ownerID, env)
	m.wg.Wait() // Wait for all goroutines to finish

	// Assert
	assert.Nil(t, err)
	assert.NotNil(t, inst)
	assert.Equal(t, "inst1", inst.ID)
	assert.Equal(t, types.StateInUse, inst.State)
	assert.Equal(t, ownerID, inst.OwnerID)
}

func TestProvision_FailedToUpdateInstanceState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	poolName := "testPool"
	ownerID := "owner1"
	env := setupMockEnvConfig()

	m := &Manager{
		instanceStore: mockInstanceStore,
		poolMap: map[string]*poolEntry{
			poolName: {Pool: Pool{Name: poolName, MinSize: 1, MaxSize: 3}},
		},
		globalCtx: context.Background(), // initialize the global context for this test
	}

	instances := []*types.Instance{
		{
			ID:    "inst1",
			State: types.StateCreated,
		},
	}

	// Mocking the calls
	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(instances, nil)
	mockInstanceStore.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("update error"))

	_, err := m.Provision(ctx, poolName, ownerID, env)

	// Assert
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to tag an instance")
}

func TestProvision_FailedHealthCheckAndDestroy(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)
	mockDriver := mocks.NewMockDriver(ctrl)
	mockLeHttpClient := mocks.NewMockLiteClient(ctrl)
	mockClientFactory := mocks.NewMockClientFactory(ctrl)

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*10) // adding a timeout to prevent hanging
	defer cancel()

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
		globalCtx:     ctx, // use the ctx created above to make sure spawned goroutines can be controlled
	}

	instances := []*types.Instance{
		{
			ID:    "inst1",
			State: types.StateCreated,
		},
	}

	newInstance := &types.Instance{
		ID:    "inst2",
		State: types.StateInUse,
	}

	// Mocking the calls
	mockInstanceStore.EXPECT().List(gomock.Any(), poolName, gomock.Any()).Return(instances, nil).Times(1)
	mockInstanceStore.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	mockClientFactory.EXPECT().NewClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(mockLeHttpClient, nil).Times(1)
	mockLeHttpClient.EXPECT().RetryHealth(gomock.Any(), gomock.Any()).Return(nil, errors.New("health check error")).Times(1)
	mockInstanceStore.EXPECT().Find(gomock.Any(), gomock.Any()).Return(instances[0], nil).Times(1)
	mockDriver.EXPECT().Destroy(gomock.Any(), gomock.Any()).Return(errors.New("destroy error")).Times(1)
	mockDriver.EXPECT().Create(gomock.Any(), gomock.Any()).Return(newInstance, nil).AnyTimes()
	mockInstanceStore.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockDriver.EXPECT().CanHibernate().Return(false).AnyTimes()

	_, err := m.Provision(ctx, poolName, ownerID, env)
	m.wg.Wait() // Wait for all goroutines to finish

	assert.NoError(t, err, "expected no error from Provision")
	assert.NotNil(t, newInstance, "expected newInstance to not be nil")
	assert.Equal(t, "inst2", newInstance.ID, "expected newInstance ID to be inst2")
	assert.Equal(t, types.StateInUse, newInstance.State, "expected newInstance state to be StateInUse")

}

func TestProvision_FailedHealthCheckSuccessfulDestroyFailedCreation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstanceStore := mocks.NewMockInstanceStore(ctrl)
	mockDriver := mocks.NewMockDriver(ctrl)
	mockLeHttpClient := mocks.NewMockLiteClient(ctrl)
	mockClientFactory := mocks.NewMockClientFactory(ctrl)

	ctx, cancel := context.WithTimeout(context.TODO(), time.Minute*10) // adding a timeout to prevent hanging
	defer cancel()

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
		globalCtx:     context.Background(), // initialize the global context for this test
	}

	instances := []*types.Instance{
		{
			ID:    "inst1",
			State: types.StateCreated,
		},
	}

	// Mocking the calls
	mockInstanceStore.EXPECT().List(ctx, poolName, gomock.Any()).Return(instances, nil).AnyTimes()
	mockInstanceStore.EXPECT().Update(ctx, gomock.Any()).Return(nil).AnyTimes()
	mockClientFactory.EXPECT().NewClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(mockLeHttpClient, nil).AnyTimes()
	mockLeHttpClient.EXPECT().RetryHealth(gomock.Any(), gomock.Any()).Return(nil, errors.New("health check error")).AnyTimes()
	mockInstanceStore.EXPECT().Find(ctx, gomock.Any()).Return(instances[0], nil).AnyTimes()
	mockDriver.EXPECT().Destroy(ctx, gomock.Any()).Return(nil).AnyTimes()
	mockInstanceStore.EXPECT().Delete(ctx, gomock.Any()).Return(nil).AnyTimes()
	mockDriver.EXPECT().Create(ctx, gomock.Any()).Return(nil, errors.New("creation error")).AnyTimes()

	_, err := m.Provision(ctx, poolName, ownerID, env)

	assert.Error(t, err, "expected an error from Provision")
	assert.Contains(t, err.Error(), "failed to create instance", "error message should contain 'failed to create instance'")
}

func TestProvision_Deadlock(t *testing.T) {
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
		globalCtx:     context.Background(), // initialize the global context for this test
	}

	instances := []*types.Instance{
		{
			ID:    "inst1",
			State: types.StateCreated,
		},
	}

	instance := &types.Instance{
		ID:    "inst2",
		State: types.StateCreated,
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
	mockDriver.EXPECT().Create(context.Background(), gomock.Any()).Return(instance, nil)
	mockInstanceStore.EXPECT().Create(context.Background(), gomock.Any()).Return(nil)
	mockDriver.EXPECT().CanHibernate().Return(false).AnyTimes()

	// Use a channel to signal the completion of the goroutine
	doneCh := make(chan bool)

	go func() {
		_, err := m.Provision(ctx, poolName, ownerID, env)
		m.wg.Wait() // Wait for all goroutines to finish
		if err != nil {
			t.Errorf("unexpected error in Provision: %v", err)
		}
		close(doneCh)
	}()

	// Set a timeout to detect deadlocks
	select {
	case <-doneCh:
		// Completed successfully
	case <-time.After(20 * time.Second):
		t.Fatal("Test timed out, potential deadlock detected!")
	}
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
