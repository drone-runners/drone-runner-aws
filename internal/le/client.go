package le

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
)

// lite engine wrapper

type LiteClient interface {
	Setup(ctx context.Context, in *api.SetupRequest) (*api.SetupResponse, error)
	Destroy(ctx context.Context, in *api.DestroyRequest) (*api.DestroyResponse, error)
	RetryStartStep(ctx context.Context, in *api.StartStepRequest) (*api.StartStepResponse, error)
	StartStep(ctx context.Context, in *api.StartStepRequest) (*api.StartStepResponse, error)
	PollStep(ctx context.Context, in *api.PollStepRequest) (*api.PollStepResponse, error)
	RetryPollStep(ctx context.Context, in *api.PollStepRequest, timeout time.Duration) (step *api.PollStepResponse, pollError error)
	GetStepLogOutput(ctx context.Context, in *api.StreamOutputRequest, w io.Writer) error
	Health(ctx context.Context) (*api.HealthResponse, error)
	RetryHealth(ctx context.Context, timeout time.Duration) (*api.HealthResponse, error)
}

type LiteClientWrapper struct {
	client lehttp.Client
}

func (l *LiteClientWrapper) Destroy(ctx context.Context, in *api.DestroyRequest) (*api.DestroyResponse, error) {
	return l.client.Destroy(ctx, in)
}

func (l *LiteClientWrapper) RetryStartStep(ctx context.Context, in *api.StartStepRequest) (*api.StartStepResponse, error) {
	return l.client.RetryStartStep(ctx, in)
}

func (l *LiteClientWrapper) StartStep(ctx context.Context, in *api.StartStepRequest) (*api.StartStepResponse, error) {
	return l.client.StartStep(ctx, in)
}

func (l *LiteClientWrapper) PollStep(ctx context.Context, in *api.PollStepRequest) (*api.PollStepResponse, error) {
	return l.client.PollStep(ctx, in)
}

func (l *LiteClientWrapper) RetryPollStep(ctx context.Context, in *api.PollStepRequest, timeout time.Duration) (step *api.PollStepResponse, pollError error) {
	return l.client.RetryPollStep(ctx, in, timeout)
}

func (l *LiteClientWrapper) GetStepLogOutput(ctx context.Context, in *api.StreamOutputRequest, w io.Writer) error {
	return l.client.GetStepLogOutput(ctx, in, w)
}

func (l *LiteClientWrapper) Health(ctx context.Context) (*api.HealthResponse, error) {
	return l.client.Health(ctx)
}

func (l *LiteClientWrapper) RetryHealth(ctx context.Context, timeout time.Duration) (*api.HealthResponse, error) {
	return l.client.RetryHealth(ctx, timeout)
}

func (l *LiteClientWrapper) Setup(ctx context.Context, in *api.SetupRequest) (*api.SetupResponse, error) {
	return l.client.Setup(ctx, in)
}

// Client

type ClientFactory interface {
	NewClient(instance *types.Instance, runnerName string, liteEnginePort int64, mock bool, mockTimeoutSecs int) (lehttp.Client, error)
}

type LiteEngineClientFactory struct{}

func (f *LiteEngineClientFactory) NewClient(instance *types.Instance, runnerName string, liteEnginePort int64, mock bool, mockTimeoutSecs int) (lehttp.Client, error) {
	leURL := fmt.Sprintf("https://%s:%d/", instance.Address, liteEnginePort)
	if mock {
		return lehttp.NewNoopClient(&api.PollStepResponse{}, nil, time.Duration(mockTimeoutSecs)*time.Second, 0, 0), nil
	}
	return lehttp.NewHTTPClient(leURL, runnerName, string(instance.CACert), string(instance.TLSCert), string(instance.TLSKey))
}
