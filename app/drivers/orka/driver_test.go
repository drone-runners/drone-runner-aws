package orka

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/types"
)

// mockClient is a func-field mock implementation of Client for testing.
type mockClient struct {
	VersionFunc  func(ctx context.Context) (*versionResponse, error)
	DeployVMFunc func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error)
	GetVMFunc    func(ctx context.Context, namespace, name string) (*getVMResponse, error)
	DeleteVMFunc func(ctx context.Context, namespace, name string) error
}

func (m *mockClient) Version(ctx context.Context) (*versionResponse, error) {
	if m.VersionFunc != nil {
		return m.VersionFunc(ctx)
	}
	return &versionResponse{}, nil
}

func (m *mockClient) DeployVM(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
	if m.DeployVMFunc != nil {
		return m.DeployVMFunc(ctx, namespace, req)
	}
	return &deployVMResponse{}, nil
}

func (m *mockClient) GetVM(ctx context.Context, namespace, name string) (*getVMResponse, error) {
	if m.GetVMFunc != nil {
		return m.GetVMFunc(ctx, namespace, name)
	}
	return &getVMResponse{}, nil
}

func (m *mockClient) DeleteVM(ctx context.Context, namespace, name string) error {
	if m.DeleteVMFunc != nil {
		return m.DeleteVMFunc(ctx, namespace, name)
	}
	return nil
}

// newTestConfig builds a *config for Create() tests, with userData defaulted the same way New()
// does it - otherwise Create() would fall through to cloudinit's generic template instead of
// exercising the real one.
func newTestConfig(mock Client) *config {
	return &config{
		namespace:  "harness-ci",
		image:      "ghcr.io/org/image:tag",
		userData:   defaultUserData,
		orkaClient: mock,
	}
}

func newTestOpts() *types.InstanceCreateOpts {
	return &types.InstanceCreateOpts{
		RunnerName: "test-runner",
		PoolName:   "test-pool",
		Platform:   types.Platform{OS: "darwin", Arch: "arm64"},
		CACert:     []byte("ca-cert"),
		CAKey:      []byte("ca-key"),
		TLSCert:    []byte("tls-cert"),
		TLSKey:     []byte("tls-key"),
	}
}

func TestCreate_Success(t *testing.T) {
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			assert.Equal(t, "9079:9079", req.ReservedPorts)
			return &deployVMResponse{IP: "10.0.0.5", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.5", inst.Address)
	assert.Equal(t, int64(primaryPort), inst.Port)
	assert.Equal(t, types.Orka, inst.Provider)
	assert.Equal(t, types.StateProvisioning, inst.State)
	assert.Equal(t, inst.ID, inst.Name)
}

func TestCreate_PortConflictFallsBackToSecondPort(t *testing.T) {
	calls := 0
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			calls++
			if req.ReservedPorts == "9079:9079" {
				return nil, &APIError{StatusCode: http.StatusInternalServerError, Status: statusModel{
					Message: "Internal error occurred: Requested reserved port range not available on the chosen node(s).",
					Reason:  "InternalError",
				}}
			}
			assert.Equal(t, "9080:9079", req.ReservedPorts)
			return &deployVMResponse{IP: "10.0.0.6", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	assert.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, int64(fallbackPort), inst.Port)
	assert.Equal(t, "10.0.0.6", inst.Address)
}

func TestCreate_RejectedDeployNeedsNoCleanup(t *testing.T) {
	deleteCalled := false
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			return nil, &APIError{StatusCode: http.StatusInternalServerError, Status: statusModel{
				Message: "Internal error occurred: Requested reserved port range not available on the chosen node(s).",
				Reason:  "InternalError",
			}}
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleteCalled = true
			return nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	assert.Error(t, err)
	assert.Nil(t, inst)
	assert.False(t, deleteCalled, "a rejected deploy (both ports unavailable) is auto-cleaned by Orka, driver must not call DeleteVM")
}

func TestCreate_NonRunningStatusTriggersExplicitDelete(t *testing.T) {
	deleteCalled := false
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			return &deployVMResponse{IP: "10.0.0.7", Status: "Failed"}, nil
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleteCalled = true
			return nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	assert.Error(t, err)
	assert.Nil(t, inst)
	assert.True(t, deleteCalled, "a deploy that completes in a non-Running terminal state must be explicitly deleted")
}

func TestCreate_DeployTimeoutTriggersExplicitDelete(t *testing.T) {
	deleteCalled := false
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			return nil, context.DeadlineExceeded
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleteCalled = true
			return nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	assert.Error(t, err)
	assert.Nil(t, inst)
	assert.True(t, deleteCalled, "a deploy call that times out client-side must be explicitly deleted, since Orka does not auto-clean timed-out deploys")
}

func TestDestroyInstanceAndStorage(t *testing.T) {
	t.Run("empty input returns nil, nil", func(t *testing.T) {
		c := &config{namespace: "harness-ci", orkaClient: &mockClient{}}
		failed, err := c.DestroyInstanceAndStorage(context.Background(), nil, nil)
		assert.NoError(t, err)
		assert.Nil(t, failed)
	})

	t.Run("all succeed returns nil, nil", func(t *testing.T) {
		deleted := map[string]bool{}
		mock := &mockClient{
			DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
				deleted[name] = true
				return nil
			},
		}
		c := &config{namespace: "harness-ci", orkaClient: mock}
		instances := []*types.Instance{{ID: "vm-1"}, {ID: "vm-2"}}

		failed, err := c.DestroyInstanceAndStorage(context.Background(), instances, nil)

		assert.NoError(t, err)
		assert.Nil(t, failed)
		assert.True(t, deleted["vm-1"])
		assert.True(t, deleted["vm-2"])
	})

	t.Run("404 is treated as already deleted, not a failure", func(t *testing.T) {
		mock := &mockClient{
			DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
				return &APIError{StatusCode: http.StatusNotFound}
			},
		}
		c := &config{namespace: "harness-ci", orkaClient: mock}
		instances := []*types.Instance{{ID: "vm-1"}}

		failed, err := c.DestroyInstanceAndStorage(context.Background(), instances, nil)

		assert.NoError(t, err)
		assert.Nil(t, failed)
	})

	t.Run("does not early-exit, accumulates all failures", func(t *testing.T) {
		mock := &mockClient{
			DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
				if name == "vm-2" {
					return nil
				}
				return errors.New("boom")
			},
		}
		c := &config{namespace: "harness-ci", orkaClient: mock}
		instances := []*types.Instance{{ID: "vm-1"}, {ID: "vm-2"}, {ID: "vm-3"}}

		failed, err := c.DestroyInstanceAndStorage(context.Background(), instances, nil)

		assert.Error(t, err)
		assert.Len(t, failed, 2)
		assert.Equal(t, "vm-1", failed[0].ID)
		assert.Equal(t, "vm-3", failed[1].ID)
	})
}

func TestResolveDeployImage(t *testing.T) {
	t.Run("vm_config wins over image when both set", func(t *testing.T) {
		c := &config{vmConfig: "sequoia_template", image: "ghcr.io/org/image:tag"}
		vmConfigField, imageField, err := c.resolveDeployImage(context.Background(), &types.VMImageConfig{})
		assert.NoError(t, err)
		assert.Equal(t, "sequoia_template", vmConfigField)
		assert.Equal(t, "", imageField)
	})

	t.Run("falls back to image when vm_config unset", func(t *testing.T) {
		c := &config{image: "ghcr.io/org/image:tag"}
		vmConfigField, imageField, err := c.resolveDeployImage(context.Background(), &types.VMImageConfig{})
		assert.NoError(t, err)
		assert.Equal(t, "", vmConfigField)
		assert.Equal(t, "ghcr.io/org/image:tag", imageField)
	})

	t.Run("per-build ImageName overrides driver default image", func(t *testing.T) {
		c := &config{image: "ghcr.io/org/default:tag"}
		vmConfigField, imageField, err := c.resolveDeployImage(context.Background(), &types.VMImageConfig{ImageName: "ghcr.io/org/override:tag"})
		assert.NoError(t, err)
		assert.Equal(t, "", vmConfigField)
		assert.Equal(t, "ghcr.io/org/override:tag", imageField)
	})
}

func TestBuildCustomMetadata(t *testing.T) {
	t.Run("driver startup_script key wins over customer-supplied collision", func(t *testing.T) {
		c := &config{customMetadata: map[string]string{
			"startup_script": "customer-supplied-should-be-overwritten",
			"owner":          "team-a",
		}}
		merged := c.buildCustomMetadata("real-script-content")

		assert.Equal(t, "team-a", merged["owner"])
		assert.NotEqual(t, "customer-supplied-should-be-overwritten", merged["startup_script"])
	})

	t.Run("does not mutate the shared pool-level customMetadata map", func(t *testing.T) {
		shared := map[string]string{"owner": "team-a"}
		c := &config{customMetadata: shared}

		c.buildCustomMetadata("script-1")

		assert.Len(t, shared, 1, "buildCustomMetadata must build a fresh map, not mutate the pool-level one shared across concurrent Create() calls")
	})
}

func TestPing(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock := &mockClient{
			VersionFunc: func(ctx context.Context) (*versionResponse, error) {
				return &versionResponse{APIVersion: "v1"}, nil
			},
		}
		c := &config{orkaClient: mock}
		assert.NoError(t, c.Ping(context.Background()))
	})

	t.Run("empty apiVersion is an error", func(t *testing.T) {
		mock := &mockClient{
			VersionFunc: func(ctx context.Context) (*versionResponse, error) {
				return &versionResponse{}, nil
			},
		}
		c := &config{orkaClient: mock}
		assert.Error(t, c.Ping(context.Background()))
	})

	t.Run("client error propagates", func(t *testing.T) {
		mock := &mockClient{
			VersionFunc: func(ctx context.Context) (*versionResponse, error) {
				return nil, errors.New("connection refused")
			},
		}
		c := &config{orkaClient: mock}
		assert.Error(t, c.Ping(context.Background()))
	})
}

func TestNew(t *testing.T) {
	t.Run("defaults userData to the embedded template and builds a real client", func(t *testing.T) {
		driver, err := New(WithNamespace("harness-ci"), WithControllerURI("https://orka.example.com"), WithAuthToken("tok"))
		assert.NoError(t, err)

		c, ok := driver.(*config)
		assert.True(t, ok)
		assert.Equal(t, defaultUserData, c.userData, "New() must default c.userData to the embedded template, not leave it empty (which would fall through to cloudinit's generic mac_arm64 template)")
		assert.NotNil(t, c.orkaClient)
	})

	t.Run("WithUserData override wins over the embedded default", func(t *testing.T) {
		driver, err := New(WithUserData("#!/bin/bash\necho custom\n", ""))
		assert.NoError(t, err)

		c, ok := driver.(*config)
		assert.True(t, ok)
		assert.Equal(t, "#!/bin/bash\necho custom\n", c.userData)
	})
}
