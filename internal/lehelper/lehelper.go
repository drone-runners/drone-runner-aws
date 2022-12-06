package lehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/api"
	"golang.org/x/crypto/ssh"
)

const (
	LiteEnginePort = 9079
)

func GenerateUserdata(userdata string, opts *types.InstanceCreateOpts) string {
	var params = cloudinit.Params{
		Platform:             opts.Platform,
		CACert:               string(opts.CACert),
		TLSCert:              string(opts.TLSCert),
		TLSKey:               string(opts.TLSKey),
		LiteEnginePath:       opts.LiteEnginePath,
		HarnessTestBinaryURI: opts.HarnessTestBinaryURI,
		PluginBinaryURI:      opts.PluginBinaryURI,
	}

	if userdata == "" {
		if opts.OS == oshelp.OSWindows {
			userdata = cloudinit.Windows(&params)
		} else if opts.OS == oshelp.OSMac {
			userdata = cloudinit.Mac(&params)
		} else {
			userdata = cloudinit.Linux(&params)
		}
	} else {
		userdata, _ = cloudinit.Custom(userdata, &params)
	}
	return userdata
}

func GetClient(instance *types.Instance, runnerName string, liteEnginePort int64) (*liteEngineSSHClient, error) {
	dat, err := os.ReadFile("/tmp/engine/harsh.pem")
	if err != nil {
		return nil, err
	}
	return &liteEngineSSHClient{Hostname: "ec2-3-129-87-150.us-east-2.compute.amazonaws.com:22", Username: "ubuntu", BaseURL: instance.Address, SSHKey: string(dat)}, nil
}

type liteEngineSSHClient struct {
	Hostname string
	Username string
	Password string
	SSHKey   string
	BaseURL  string
}

func (l *liteEngineSSHClient) Setup(ctx context.Context, in *api.SetupRequest) (*api.SetupResponse, error) {
	client, err := dial(l.Hostname, l.Username, l.Password, l.SSHKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	cmd := fmt.Sprintf("curl -H \"Content-Type: application/json\" -X POST %s/setup -d '%s'", l.BaseURL, string(b))
	fmt.Printf("cmd is: %s\n", cmd)
	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf
	if err = session.Run(cmd); err != nil {
		fmt.Printf("stdout/stderr logs: %s", string(b))
		return nil, err
	}
	fmt.Printf("stdout/stderr logs: %s", string(b))
	return &api.SetupResponse{}, nil
}

func (l *liteEngineSSHClient) Destroy(ctx context.Context, in *api.DestroyRequest) (*api.DestroyResponse, error) {
	client, err := dial(l.Hostname, l.Username, l.Password, l.SSHKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	cmd := fmt.Sprintf("curl -H \"Content-Type: application/json\" -X POST %s/destroy -d '%s'", l.BaseURL, string(b))
	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf
	if err = session.Run(cmd); err != nil {
		fmt.Printf("stdout/stderr logs: %s", string(b))
		return nil, err
	}
	fmt.Printf("stdout/stderr logs: %s", string(b))
	return &api.DestroyResponse{}, nil
}

func (l *liteEngineSSHClient) StartStep(ctx context.Context, in *api.StartStepRequest) (*api.StartStepResponse, error) {
	client, err := dial(l.Hostname, l.Username, l.Password, l.SSHKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	cmd := fmt.Sprintf("curl -H \"Content-Type: application/json\" -X POST %s/start_step -d '%s'", l.BaseURL, string(b))
	fmt.Printf("cmd is: %s\n", cmd)
	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf
	if err = session.Run(cmd); err != nil {
		fmt.Printf("stdout/stderr logs: %s", string(b))
		return nil, err
	}
	fmt.Printf("stdout/stderr logs: %s", string(b))
	return nil, nil
}

func (l *liteEngineSSHClient) PollStep(ctx context.Context, in *api.PollStepRequest) (*api.PollStepResponse, error) {
	client, err := dial(l.Hostname, l.Username, l.Password, l.SSHKey)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	cmd := fmt.Sprintf("curl -H \"Content-Type: application/json\" -X POST %s/poll_step -d '%s'", l.BaseURL, string(b))
	fmt.Printf("cmd is: %s\n", cmd)
	output, err := session.Output(cmd)
	if err != nil {
		fmt.Printf("output: %s\n", string(output))
		return nil, err
	}
	fmt.Printf("output: %s\n", output)
	// Try to unmarshal the response
	resp := &api.PollStepResponse{}
	err = json.Unmarshal(output, resp)
	if err != nil {
		fmt.Printf("error while unmarshaling: %s\n", err)
	}
	return resp, nil
}

func (l *liteEngineSSHClient) RetryPollStep(ctx context.Context, in *api.PollStepRequest, timeout time.Duration) (step *api.PollStepResponse, pollError error) {
	startTime := time.Now()
	retryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for i := 0; ; i++ {
		select {
		case <-retryCtx.Done():
			return step, retryCtx.Err()
		default:
		}
		step, pollError = l.PollStep(retryCtx, in)
		fmt.Printf("step: %+v\n", step)
		fmt.Printf("pollError: %s\n", pollError)
		if pollError == nil {
			logger.FromContext(retryCtx).
				WithField("duration", time.Since(startTime)).
				Trace("RetryPollStep: step completed")
			return step, pollError
		}
		time.Sleep(time.Millisecond * 10) //nolint:gomnd
	}
}

func (l *liteEngineSSHClient) GetStepLogOutput(ctx context.Context, in *api.StreamOutputRequest, w io.Writer) error {
	return nil
}

func (l *liteEngineSSHClient) Health(ctx context.Context) (*api.HealthResponse, error) {
	time.Sleep(2 * time.Second)
	return &api.HealthResponse{OK: true}, nil
}

func (l *liteEngineSSHClient) RetryHealth(ctx context.Context, timeout time.Duration) (*api.HealthResponse, error) {
	return l.Health(ctx)
}

// helper function configures and dials the ssh server.
func dial(server, username, password, privatekey string) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User:            username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	if privatekey != "" {
		pem := []byte(privatekey)
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return nil, err
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	return ssh.Dial("tcp", server, config)
}
