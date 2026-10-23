package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
)

// fakeDriver records what the lifecycle layer asks of the driver. Methods it
// does not override panic via the nil embedded interface, which proves the
// lifecycle path touches nothing else.
type fakeDriver struct {
	drivers.Driver

	createOpts    *types.InstanceCreateOpts
	createResult  *types.Instance
	createErr     error
	destroyed     []*types.Instance
	destroyErr    error
	destroyCalled int
}

func (f *fakeDriver) DriverName() string { return string(types.Google) }

func (f *fakeDriver) Create(_ context.Context, opts *types.InstanceCreateOpts) (*types.Instance, error) {
	f.createOpts = opts
	return f.createResult, f.createErr
}

func (f *fakeDriver) DestroyInstanceAndStorage(_ context.Context, insts []*types.Instance, cleanup *storage.CleanupType) ([]*types.Instance, error) {
	f.destroyCalled++
	f.destroyed = insts
	if cleanup != nil {
		return nil, errors.New("storage cleanup type must be nil")
	}
	return nil, f.destroyErr
}

func newPool(d drivers.Driver) *drivers.Pool {
	return &drivers.Pool{
		Name:     "pool-a",
		Platform: types.Platform{OS: "linux", Arch: "amd64"},
		Driver:   d,
		Spec:     &config.Google{},
	}
}

func createRequest(pool *drivers.Pool) CreateRequest {
	return CreateRequest{
		Pool:            pool,
		RunnerName:      "runner-1",
		AccountID:       "acct1",
		DelegatePath:    "https://dl.example.com/delegates/3.9.0",
		Token:           "tok",
		ManagerEndpoint: "https://app.harness.io",
		TransactionID:   "txn1",
		DelegateName:    "vm-delegate",
	}
}

func TestCreate(t *testing.T) {
	t.Run("returns driver state and requests an ephemeral create", func(t *testing.T) {
		fd := &fakeDriver{createResult: &types.Instance{
			ID:                "123",
			Name:              "vm-1",
			Provider:          types.Google,
			Zone:              "us-central1-a",
			StorageIdentifier: "disk-1",
		}}
		state, err := Create(context.Background(), createRequest(newPool(fd)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		want := DriverState{
			Version:           driverStateVersion,
			ID:                "123",
			Name:              "vm-1",
			Provider:          "google",
			PoolName:          "pool-a",
			Zone:              "us-central1-a",
			StorageIdentifier: "disk-1",
			TenantID:          types.DefaultTenantID,
		}
		if state != want {
			t.Errorf("state = %+v, want %+v", state, want)
		}
		if fd.createOpts == nil || !fd.createOpts.EphemeralDelegate {
			t.Fatalf("driver was not asked for an ephemeral create: %+v", fd.createOpts)
		}
		if fd.createOpts.DelegateToken != "tok" || fd.createOpts.DelegateTransactionID != "txn1" {
			t.Errorf("delegate fields not passed to the driver: %+v", fd.createOpts)
		}
	})

	t.Run("instance tenant id wins over the resolved one", func(t *testing.T) {
		fd := &fakeDriver{createResult: &types.Instance{ID: "123", Name: "vm-1", TenantID: "tenant-x"}}
		state, err := Create(context.Background(), createRequest(newPool(fd)))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if state.TenantID != "tenant-x" {
			t.Errorf("TenantID = %q, want tenant-x", state.TenantID)
		}
	})

	t.Run("driver error is returned", func(t *testing.T) {
		fd := &fakeDriver{createErr: errors.New("stockout")}
		if _, err := Create(context.Background(), createRequest(newPool(fd))); err == nil || !strings.Contains(err.Error(), "stockout") {
			t.Fatalf("error = %v, want stockout", err)
		}
	})

	t.Run("instance without an id is an error", func(t *testing.T) {
		fd := &fakeDriver{createResult: &types.Instance{}}
		if _, err := Create(context.Background(), createRequest(newPool(fd))); err == nil {
			t.Fatal("want error for an instance with no id")
		}
	})

	t.Run("invalid request never reaches the driver", func(t *testing.T) {
		fd := &fakeDriver{}
		req := createRequest(newPool(fd))
		req.DelegatePath = ""
		if _, err := Create(context.Background(), req); err == nil {
			t.Fatal("want error for missing delegate path")
		}
		if fd.createOpts != nil {
			t.Error("driver Create must not be called for an invalid request")
		}
	})
}

func TestDestroy(t *testing.T) {
	state := DriverState{
		Version:           driverStateVersion,
		ID:                "123",
		Name:              "vm-1",
		Provider:          "google",
		PoolName:          "pool-a",
		Zone:              "us-central1-a",
		StorageIdentifier: "disk-1",
		TenantID:          "tenant-x",
	}

	t.Run("destroys the instance described by the state", func(t *testing.T) {
		fd := &fakeDriver{}
		if err := Destroy(context.Background(), newPool(fd), state); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
		if fd.destroyCalled != 1 || len(fd.destroyed) != 1 {
			t.Fatalf("destroy calls = %d, instances = %d", fd.destroyCalled, len(fd.destroyed))
		}
		got := fd.destroyed[0]
		if got.ID != "123" || got.Name != "vm-1" || got.Zone != "us-central1-a" ||
			got.Provider != types.Google || got.Pool != "pool-a" ||
			got.StorageIdentifier != "disk-1" || got.TenantID != "tenant-x" {
			t.Errorf("instance passed to driver = %+v", got)
		}
	})

	t.Run("missing tenant id falls back to the default tenant", func(t *testing.T) {
		fd := &fakeDriver{}
		s := state
		s.TenantID = ""
		if err := Destroy(context.Background(), newPool(fd), s); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
		if fd.destroyed[0].TenantID != types.DefaultTenantID {
			t.Errorf("TenantID = %q, want %q", fd.destroyed[0].TenantID, types.DefaultTenantID)
		}
	})

	t.Run("driver error is returned", func(t *testing.T) {
		fd := &fakeDriver{destroyErr: errors.New("delete failed")}
		if err := Destroy(context.Background(), newPool(fd), state); err == nil || !strings.Contains(err.Error(), "delete failed") {
			t.Fatalf("error = %v, want delete failed", err)
		}
	})

	t.Run("nil pool is an error", func(t *testing.T) {
		if err := Destroy(context.Background(), nil, state); err == nil {
			t.Fatal("want error for nil pool")
		}
	})

	t.Run("empty id is an error and never reaches the driver", func(t *testing.T) {
		fd := &fakeDriver{}
		s := state
		s.ID = ""
		if err := Destroy(context.Background(), newPool(fd), s); err == nil {
			t.Fatal("want error for empty id")
		}
		if fd.destroyCalled != 0 {
			t.Error("driver must not be called without an id")
		}
	})
}
