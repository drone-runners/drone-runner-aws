package harness

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/types"
)

func preserveTestPoolManager(updated *[]*types.Instance) *testPoolManager {
	return &testPoolManager{
		findFunc: func(_ context.Context, id string) (*types.Instance, error) {
			return &types.Instance{ID: id, State: types.StateInUse}, nil
		},
		updateFunc: func(_ context.Context, instance *types.Instance) error {
			if updated != nil {
				*updated = append(*updated, instance)
			}
			return nil
		},
	}
}

func TestRecoverStartStepAfterTimeout_RetrySucceeds(t *testing.T) {
	t.Parallel()

	var updated []*types.Instance
	pm := preserveTestPoolManager(&updated)

	calls := 0
	client := &mockLEClient{
		healthFunc: func(context.Context, *api.HealthRequest) (*api.HealthResponse, error) {
			return &api.HealthResponse{OK: true, Version: "1.2.3"}, nil
		},
		retryStartStepFunc: func(context.Context, *api.StartStepRequest, time.Duration) (*api.StartStepResponse, error) {
			calls++
			if calls < 2 {
				return nil, context.DeadlineExceeded
			}
			return &api.StartStepResponse{}, nil
		},
	}

	inst := &types.Instance{ID: "inst-1", State: types.StateInUse}
	resp, err := recoverStartStepAfterTimeout(context.Background(), &ExecuteVMRequest{}, inst, client, pm, logrusEntry())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 2, calls, "should retry until success")

	require.NotEmpty(t, updated, "instance must be preserved")
	assert.Equal(t, types.StatePreserved, updated[0].State)
}

func TestRecoverStartStepAfterTimeout_AllRetriesFail(t *testing.T) {
	t.Parallel()

	var updated []*types.Instance
	pm := preserveTestPoolManager(&updated)

	calls := 0
	client := &mockLEClient{
		healthFunc: func(context.Context, *api.HealthRequest) (*api.HealthResponse, error) {
			return &api.HealthResponse{OK: false}, nil
		},
		retryStartStepFunc: func(context.Context, *api.StartStepRequest, time.Duration) (*api.StartStepResponse, error) {
			calls++
			return nil, context.DeadlineExceeded
		},
	}

	inst := &types.Instance{ID: "inst-1", State: types.StateInUse}
	resp, err := recoverStartStepAfterTimeout(context.Background(), &ExecuteVMRequest{}, inst, client, pm, logrusEntry())
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, maxStartStepDebugRetries, calls, "should retry exactly maxStartStepDebugRetries times")
	assert.Contains(t, err.Error(), "after 5 debug retries")

	require.NotEmpty(t, updated, "instance must still be preserved even when retries fail")
	assert.Equal(t, types.StatePreserved, updated[0].State)
}

func TestRecoverStartStepAfterTimeout_HealthFailureDoesNotBlockRetry(t *testing.T) {
	t.Parallel()

	pm := preserveTestPoolManager(nil)
	client := &mockLEClient{
		healthFunc: func(context.Context, *api.HealthRequest) (*api.HealthResponse, error) {
			return nil, errors.New("health endpoint unreachable")
		},
		retryStartStepFunc: func(context.Context, *api.StartStepRequest, time.Duration) (*api.StartStepResponse, error) {
			return &api.StartStepResponse{}, nil
		},
	}

	inst := &types.Instance{ID: "inst-1", State: types.StateInUse}
	resp, err := recoverStartStepAfterTimeout(context.Background(), &ExecuteVMRequest{}, inst, client, pm, logrusEntry())
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestEnableVerboseHTTP_WrapsTransportOnce(t *testing.T) {
	t.Parallel()

	hc := &lehttp.HTTPClient{Client: &http.Client{}, Endpoint: "https://localhost:9079/"}

	enableVerboseHTTP(hc, logrusEntry())
	vrt, ok := hc.Client.Transport.(*verboseRoundTripper)
	require.True(t, ok, "transport must be wrapped with verboseRoundTripper")

	// Calling again must not double-wrap.
	enableVerboseHTTP(hc, logrusEntry())
	vrt2, ok := hc.Client.Transport.(*verboseRoundTripper)
	require.True(t, ok)
	assert.Same(t, vrt, vrt2, "transport must not be re-wrapped")
	_, nested := vrt2.base.(*verboseRoundTripper)
	assert.False(t, nested, "base transport must not itself be a verboseRoundTripper")
}

func TestEnableVerboseHTTP_NonHTTPClientNoop(t *testing.T) {
	t.Parallel()

	// Should not panic for a non-HTTPClient implementation.
	assert.NotPanics(t, func() {
		enableVerboseHTTP(&mockLEClient{}, logrusEntry())
	})
}

func TestVerboseRoundTripper_RoundTrip(t *testing.T) {
	t.Parallel()

	stub := &stubRoundTripper{
		resp: &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader("pong")),
			ContentLength: 4,
		},
	}
	vrt := &verboseRoundTripper{base: stub, logr: logrusEntry()}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/healthz", http.NoBody)
	require.NoError(t, err)

	resp, err := vrt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()
	assert.True(t, stub.called, "base round tripper must be invoked")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(body), "response body must be preserved for the caller")
}

func TestVerboseRoundTripper_RoundTripError(t *testing.T) {
	t.Parallel()

	stub := &stubRoundTripper{err: errors.New("dial tcp: connection refused")}
	vrt := &verboseRoundTripper{base: stub, logr: logrusEntry()}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/healthz", http.NoBody)
	require.NoError(t, err)

	resp, err := vrt.RoundTrip(req) //nolint:bodyclose // resp is nil on error
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.True(t, stub.called)
}

// --- test doubles ---

type stubRoundTripper struct {
	called bool
	resp   *http.Response
	err    error
}

func (s *stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	s.called = true
	return s.resp, s.err
}

type mockLEClient struct {
	healthFunc         func(ctx context.Context, in *api.HealthRequest) (*api.HealthResponse, error)
	retryStartStepFunc func(ctx context.Context, in *api.StartStepRequest, timeout time.Duration) (*api.StartStepResponse, error)
}

func (m *mockLEClient) Health(ctx context.Context, in *api.HealthRequest) (*api.HealthResponse, error) {
	if m.healthFunc != nil {
		return m.healthFunc(ctx, in)
	}
	return &api.HealthResponse{}, nil
}

func (m *mockLEClient) RetryStartStep(ctx context.Context, in *api.StartStepRequest, timeout time.Duration) (*api.StartStepResponse, error) {
	if m.retryStartStepFunc != nil {
		return m.retryStartStepFunc(ctx, in, timeout)
	}
	return &api.StartStepResponse{}, nil
}

func (m *mockLEClient) Setup(context.Context, *api.SetupRequest) (*api.SetupResponse, error) {
	return &api.SetupResponse{}, nil
}
func (m *mockLEClient) RetrySetup(context.Context, *api.SetupRequest, time.Duration) (*api.SetupResponse, error) {
	return &api.SetupResponse{}, nil
}
func (m *mockLEClient) Destroy(context.Context, *api.DestroyRequest) (*api.DestroyResponse, error) {
	return &api.DestroyResponse{}, nil
}
func (m *mockLEClient) StartStep(context.Context, *api.StartStepRequest) (*api.StartStepResponse, error) {
	return &api.StartStepResponse{}, nil
}
func (m *mockLEClient) PollStep(context.Context, *api.PollStepRequest) (*api.PollStepResponse, error) {
	return &api.PollStepResponse{}, nil
}
func (m *mockLEClient) RetryPollStep(context.Context, *api.PollStepRequest, time.Duration) (*api.PollStepResponse, error) {
	return &api.PollStepResponse{}, nil
}
func (m *mockLEClient) GetStepLogOutput(context.Context, *api.StreamOutputRequest, io.Writer) error {
	return nil
}
func (m *mockLEClient) RetryHealth(context.Context, *api.HealthRequest) (*api.HealthResponse, error) {
	return &api.HealthResponse{}, nil
}
func (m *mockLEClient) RetrySuspend(context.Context, *api.SuspendRequest, time.Duration) (*api.SuspendResponse, error) {
	return &api.SuspendResponse{}, nil
}

var _ lehttp.Client = (*mockLEClient)(nil)
