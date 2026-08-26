package harness

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/harness/lite-engine/api"
	"github.com/harness/lite-engine/pc"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestNormalizePrivateConnectivityEnvs(t *testing.T) {
	tests := []struct {
		name      string
		envs      map[string]string
		requested bool
		wantEnvs  map[string]string
	}{
		{"absent", map[string]string{"CI": "true"}, false, map[string]string{"CI": "true"}},
		{"false", map[string]string{pc.EnvEnabled: "false"}, false, map[string]string{}},
		{"false with payload", map[string]string{
			pc.EnvEnabled: "false", pc.EnvOIDCToken: "token",
		}, true, map[string]string{pc.EnvEnabled: "false", pc.EnvOIDCToken: "token"}},
		{"true", map[string]string{pc.EnvEnabled: "true"}, true,
			map[string]string{pc.EnvEnabled: "true"}},
		{"partial", map[string]string{pc.EnvOIDCToken: "token"}, true,
			map[string]string{pc.EnvOIDCToken: "token"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.requested, normalizePrivateConnectivityEnvs(tt.envs))
			require.Equal(t, tt.wantEnvs, tt.envs)
		})
	}
}

type setupClient struct {
	err   error
	calls *int
}

func (c setupClient) Setup(context.Context, *api.SetupRequest) (*api.SetupResponse, error) {
	if c.calls != nil {
		*c.calls++
	}
	return &api.SetupResponse{}, c.err
}

type stageOwnerStore struct {
	createErr error
	created   *types.StageOwner
}

func (s *stageOwnerStore) Find(context.Context, string) (*types.StageOwner, error) { return nil, nil }
func (s *stageOwnerStore) Create(_ context.Context, owner *types.StageOwner) error {
	s.created = owner
	return s.createErr
}
func (s *stageOwnerStore) Delete(context.Context, string) error { return nil }

func TestRunLiteEngineSetupIsOneShot(t *testing.T) {
	owners := new(stageOwnerStore)
	setupCalls := 0
	err := runLiteEngineSetup(context.Background(), setupClient{calls: &setupCalls}, &api.SetupRequest{},
		owners, "stage", "pool", "instance", time.Second)
	require.NoError(t, err)
	require.Equal(t, 1, setupCalls)
	require.Equal(t, &types.StageOwner{StageID: "stage", PoolName: "pool", InstanceID: "instance"}, owners.created)

	owners.createErr = fmt.Errorf("%w: duplicate stage", store.ErrConflict)
	setupCalls = 0
	err = runLiteEngineSetup(context.Background(), setupClient{calls: &setupCalls}, &api.SetupRequest{},
		owners, "stage", "pool", "instance", time.Second)
	require.ErrorIs(t, err, errPrivateConnectivitySetupClaimConflict)
	require.Zero(t, setupCalls)

	owners.createErr = errors.New("database unavailable")
	setupCalls = 0
	err = runLiteEngineSetup(context.Background(), setupClient{calls: &setupCalls}, &api.SetupRequest{},
		owners, "stage", "pool", "instance", time.Second)
	require.Error(t, err)
	require.NotErrorIs(t, err, errPrivateConnectivitySetupClaimConflict)
	require.Zero(t, setupCalls)

	owners.createErr = nil
	setupCalls = 0
	err = runLiteEngineSetup(context.Background(), setupClient{err: errors.New("connection reset"), calls: &setupCalls},
		&api.SetupRequest{}, owners, "stage", "pool", "instance", time.Second)
	require.ErrorIs(t, err, errPrivateConnectivitySetupIndeterminate)
	require.Equal(t, 1, setupCalls)
}
