package orka

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			// Default timeout (10m / 600s) must reach Orka as 10 minutes, not 600.
			assert.Equal(t, 10, req.Timeout)
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

func TestDeployTimeoutMinutes(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    int
	}{
		{"zero uses default", 0, 0},
		{"negative uses default", -5, 0},
		{"sub-minute rounds up to 1", 30, 1},
		{"exact minute", 60, 1},
		{"rounds up", 90, 2},
		{"default 600s is 10m", 600, 10},
		{"example 1200s is 20m", 1200, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, deployTimeoutMinutes(tc.seconds))
		})
	}
}

func TestCreate_CustomTimeoutConvertedToMinutes(t *testing.T) {
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			assert.Equal(t, 20, req.Timeout) // 1200s -> 20m
			return &deployVMResponse{IP: "10.0.0.7", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)
	c.timeoutSec = 1200

	_, err := c.Create(context.Background(), newTestOpts())
	assert.NoError(t, err)
}

func TestCreate_PortConflictRetriesUnderFreshName(t *testing.T) {
	var names []string
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			if req.ReservedPorts == "9079:9079" {
				return nil, &APIError{StatusCode: http.StatusInternalServerError, Status: statusModel{
					Message: "Internal error occurred: Requested reserved port range not available on the chosen node(s).",
				}}
			}
			return &deployVMResponse{IP: "10.0.0.6", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	require.NoError(t, err)
	require.Len(t, names, 2)
	assert.NotEqual(t, names[0], names[1], "retry must not reuse the name Orka is still deleting")
	assert.Equal(t, names[1], inst.ID, "instance must carry the name of the successful attempt")
	assert.Equal(t, names[1], inst.Name)
}

func TestCreate_TimeoutAfterRetryDeletesLatestName(t *testing.T) {
	var names []string
	var deleted string
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			if req.ReservedPorts == "9079:9079" {
				return nil, &APIError{StatusCode: http.StatusInternalServerError, Status: statusModel{
					Message: "Internal error occurred: Requested reserved port range not available on the chosen node(s).",
				}}
			}
			return nil, context.DeadlineExceeded
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleted = name
			return nil
		},
	}
	c := newTestConfig(mock)

	_, err := c.Create(context.Background(), newTestOpts())

	require.Error(t, err)
	require.Len(t, names, 2)
	assert.Equal(t, names[1], deleted, "cleanup must delete the retry's VM, not the rejected first one")
}

func portConflictErr() error {
	return &APIError{StatusCode: http.StatusInternalServerError, Status: statusModel{
		Message: "Internal error occurred: Requested reserved port range not available on the chosen node(s).",
		Reason:  "InternalError",
	}}
}

func TestCreate_RetryKeepsRequestExceptNameAndPort(t *testing.T) {
	var reqs []deployVMRequest
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			reqs = append(reqs, *req) // copy: the driver reuses one request
			if len(reqs) == 1 {
				return nil, portConflictErr()
			}
			return &deployVMResponse{IP: "10.0.0.6", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)

	_, err := c.Create(context.Background(), newTestOpts())

	require.NoError(t, err)
	require.Len(t, reqs, 2)
	assert.Equal(t, "9079:9079", reqs[0].ReservedPorts)
	assert.Equal(t, "9080:9079", reqs[1].ReservedPorts)
	assert.NotEqual(t, reqs[0].Name, reqs[1].Name)

	// Everything else must be identical, or the retry would deploy a different VM.
	a, b := reqs[0], reqs[1]
	a.Name, b.Name = "", ""
	a.ReservedPorts, b.ReservedPorts = "", ""
	assert.Equal(t, a, b)
}

func TestCreate_RetryNameIsValidAndPrefixed(t *testing.T) {
	var names []string
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			if len(names) == 1 {
				return nil, portConflictErr()
			}
			return &deployVMResponse{IP: "10.0.0.6", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)

	_, err := c.Create(context.Background(), newTestOpts())

	require.NoError(t, err)
	require.Len(t, names, 2)
	for _, n := range names {
		assert.Regexp(t, `^test-runner-test-pool-[a-z0-9]{8}$`, n, "Orka needs RFC 1123 lowercase names")
	}
}

func TestCreate_NonPortErrorDoesNotRetry(t *testing.T) {
	calls := 0
	deleteCalled := false
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			calls++
			return nil, &APIError{StatusCode: http.StatusConflict, Status: statusModel{Message: "already exists"}}
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleteCalled = true
			return nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	require.Error(t, err)
	assert.Nil(t, inst)
	assert.Equal(t, 1, calls)
	assert.False(t, deleteCalled)
}

func TestCreate_BothPortsConflictUsesTwoNamesAndNoDelete(t *testing.T) {
	var names []string
	deleteCalled := false
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			return nil, portConflictErr()
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleteCalled = true
			return nil
		},
	}
	c := newTestConfig(mock)

	inst, err := c.Create(context.Background(), newTestOpts())

	require.Error(t, err)
	assert.Nil(t, inst)
	assert.Contains(t, err.Error(), "after port retry")
	require.Len(t, names, 2)
	assert.NotEqual(t, names[0], names[1])
	assert.False(t, deleteCalled, "Orka auto-deletes rejected deploys")
}

func TestCreate_FirstAttemptTimeoutDeletesFirstName(t *testing.T) {
	var names []string
	var deleted []string
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			return nil, context.DeadlineExceeded
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleted = append(deleted, name)
			return nil
		},
	}
	c := newTestConfig(mock)

	_, err := c.Create(context.Background(), newTestOpts())

	require.Error(t, err)
	require.Len(t, names, 1, "a timeout is not a port conflict, so no retry")
	assert.Equal(t, names, deleted)
}

func TestCreate_NonRunningAfterRetryDeletesRetryName(t *testing.T) {
	var names []string
	var deleted []string
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			if len(names) == 1 {
				return nil, portConflictErr()
			}
			return &deployVMResponse{IP: "10.0.0.6", Status: "Failed"}, nil
		},
		DeleteVMFunc: func(ctx context.Context, namespace, name string) error {
			deleted = append(deleted, name)
			return nil
		},
	}
	c := newTestConfig(mock)

	_, err := c.Create(context.Background(), newTestOpts())

	require.Error(t, err)
	require.Len(t, names, 2)
	assert.Equal(t, []string{names[1]}, deleted)
	assert.Contains(t, err.Error(), names[1], "error should name the VM that actually failed")
}

func TestCreate_CanceledContextAfterRetryStillDeletesRetryName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var names []string
	var deleted []string
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			names = append(names, req.Name)
			if len(names) == 1 {
				return nil, portConflictErr()
			}
			cancel()
			return nil, context.Canceled
		},
		DeleteVMFunc: func(dctx context.Context, namespace, name string) error {
			assert.NoError(t, dctx.Err(), "cleanup must not reuse the canceled context")
			deleted = append(deleted, name)
			return nil
		},
	}
	c := newTestConfig(mock)

	_, err := c.Create(ctx, newTestOpts())

	require.Error(t, err)
	require.Len(t, names, 2)
	assert.Equal(t, []string{names[1]}, deleted)
}

func TestCreate_ConcurrentRetriesGetDistinctNames(t *testing.T) {
	const n = 20
	var mu sync.Mutex
	seen := map[string]int{}
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			mu.Lock()
			seen[req.Name]++
			mu.Unlock()
			if req.ReservedPorts == "9079:9079" {
				return nil, portConflictErr()
			}
			return &deployVMResponse{IP: "10.0.0.6", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)

	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inst, err := c.Create(context.Background(), newTestOpts())
			if assert.NoError(t, err) {
				ids[i] = inst.ID
			}
		}(i)
	}
	wg.Wait()

	assert.Len(t, seen, 2*n, "every attempt must use its own name")
	for name, count := range seen {
		assert.Equal(t, 1, count, name)
	}
	uniq := map[string]bool{}
	for _, id := range ids {
		uniq[id] = true
	}
	assert.Len(t, uniq, n)
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

func TestBuildCustomMetadataWithoutScript(t *testing.T) {
	t.Run("forwards customer keys but never startup_script", func(t *testing.T) {
		c := &config{customMetadata: map[string]string{
			"startup_script": "customer-supplied",
			"owner":          "team-a",
		}}
		merged := c.buildCustomMetadataWithoutScript()

		assert.Equal(t, map[string]string{"owner": "team-a"}, merged)
	})

	t.Run("does not mutate the shared pool-level customMetadata map", func(t *testing.T) {
		shared := map[string]string{"startup_script": "x", "owner": "team-a"}
		c := &config{customMetadata: shared}

		c.buildCustomMetadataWithoutScript()

		assert.Len(t, shared, 2)
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

// deliveryCapture runs Create() with enable_userdata set or not and returns the deploy request.
// Version() must never be called: delivery is a pool setting, not detected from the controller.
func deliveryCapture(t *testing.T, userdata bool, setup func(*config)) *deployVMRequest {
	t.Helper()
	var captured *deployVMRequest
	mock := &mockClient{
		VersionFunc: func(ctx context.Context) (*versionResponse, error) {
			t.Error("Create must not call /version")
			return &versionResponse{APIVersion: "4.0.0"}, nil
		},
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			captured = req
			return &deployVMResponse{IP: "10.0.0.5", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)
	c.enableUserdata = userdata
	if setup != nil {
		setup(c)
	}
	_, err := c.Create(context.Background(), newTestOpts())
	require.NoError(t, err)
	require.NotNil(t, captured)
	return captured
}

func TestCreate_ScriptDelivery(t *testing.T) {
	cases := []struct {
		name         string
		enable       bool
		wantUserdata bool
	}{
		{"default uses customMetadata", false, false},
		{"enable_userdata uses native User Data", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := deliveryCapture(t, tc.enable, nil)

			script, hasScriptKey := req.CustomMetadata[startupScriptKey]
			if tc.wantUserdata {
				assert.NotEmpty(t, req.Userdata)
				assert.False(t, hasScriptKey, "startup_script must NOT be sent alongside native User Data")
			} else {
				assert.Empty(t, req.Userdata, "native User Data must not be sent")
				assert.True(t, hasScriptKey)
				assert.NotEmpty(t, script)
			}
			assert.False(t, req.Userdata != "" && hasScriptKey, "bootstrap script sent through both channels")
		})
	}
}

func TestCreate_DefaultRequestBodyHasNoUserdataKey(t *testing.T) {
	req := deliveryCapture(t, false, nil)

	body, err := json.Marshal(req)

	require.NoError(t, err)
	assert.NotContains(t, string(body), `"userdata"`)
	assert.Contains(t, string(body), `"customMetadata"`)
}

func TestCreate_ScriptDeliveryPayloadsCarrySameScript(t *testing.T) {
	metaReq := deliveryCapture(t, false, nil)
	udReq := deliveryCapture(t, true, nil)

	metaScript, err := base64.StdEncoding.DecodeString(metaReq.CustomMetadata[startupScriptKey])
	require.NoError(t, err)
	udScript, err := base64.StdEncoding.DecodeString(udReq.Userdata)
	require.NoError(t, err)

	require.NotEmpty(t, metaScript)
	// Same opts in, so the rendered scripts must be identical.
	assert.Equal(t, string(metaScript), string(udScript))
}

func TestCreate_UserdataModeCustomMetadata(t *testing.T) {
	t.Run("forwards customer keys and strips a customer-supplied startup_script", func(t *testing.T) {
		req := deliveryCapture(t, true, func(c *config) {
			c.customMetadata = map[string]string{"owner": "team-a", "startup_script": "customer-supplied"}
		})

		assert.Equal(t, map[string]string{"owner": "team-a"}, req.CustomMetadata)
		assert.NotEmpty(t, req.Userdata)
	})

	t.Run("no customer metadata leaves customMetadata empty", func(t *testing.T) {
		req := deliveryCapture(t, true, nil)

		assert.Empty(t, req.CustomMetadata)
	})
}

func TestCreate_UserdataModeKeepsPayloadAcrossPortRetry(t *testing.T) {
	var reqs []deployVMRequest
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			reqs = append(reqs, *req)
			if len(reqs) == 1 {
				return nil, portConflictErr()
			}
			return &deployVMResponse{IP: "10.0.0.6", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)
	c.enableUserdata = true
	c.customMetadata = map[string]string{startupScriptKey: "customer", "team": "ci"}

	_, err := c.Create(context.Background(), newTestOpts())

	require.NoError(t, err)
	require.Len(t, reqs, 2)
	assert.NotEmpty(t, reqs[0].Userdata)
	assert.Equal(t, reqs[0].Userdata, reqs[1].Userdata)
	assert.Equal(t, reqs[0].CustomMetadata, reqs[1].CustomMetadata)
	assert.Equal(t, map[string]string{"team": "ci"}, reqs[1].CustomMetadata, "startup_script must not be sent in User Data mode")
}

func TestCreate_UserdataModeRejectsEmptyScript(t *testing.T) {
	deployed := false
	mock := &mockClient{
		DeployVMFunc: func(ctx context.Context, namespace string, req *deployVMRequest) (*deployVMResponse, error) {
			deployed = true
			return &deployVMResponse{IP: "10.0.0.5", Status: vmStatusRunning}, nil
		},
	}
	c := newTestConfig(mock)
	c.enableUserdata = true
	c.userData = "{{ if false }}x{{ end }}"

	inst, err := c.Create(context.Background(), newTestOpts())

	require.Error(t, err)
	assert.Nil(t, inst)
	assert.False(t, deployed, "no VM may be deployed without a bootstrap script")
}
