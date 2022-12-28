package lehelper

import (
	"fmt"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	lehttp "github.com/harness/lite-engine/cli/client"
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

func GetClient(instance *types.Instance, runnerName string, liteEnginePort int64) (*lehttp.HTTPClient, error) {
	var leURL string
	if instance.Provider == types.Nomad {
		leURL = instance.Address
	} else {
		leURL = fmt.Sprintf("https://%s:%d/", instance.Address, liteEnginePort)
	}
	return lehttp.NewHTTPClient(leURL,
		runnerName, string(instance.CACert),
		string(instance.TLSCert), string(instance.TLSKey))
}

// func GetClient(instance *types.Instance, runnerName string, liteEnginePort int64) (*liteEngineNomadClient, error) {
// 	l := strings.Split(instance.ID, "/")
// 	fmt.Println("l is: ", l)
// 	node := l[1]
// 	fmt.Println("node is: ", node)
// 	fmt.Println("zone (address) is: ", instance.Zone)
// 	fmt.Println("base url is: ", instance.Address)
// 	// TODO: Change zone
// 	config := &nomad.Config{
// 		Address: instance.Zone,
// 	}
// 	c, err := nomad.NewClient(config)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &liteEngineNomadClient{Address: instance.Zone, NodeID: node, BaseURL: instance.Address, Client: c}, nil
// }

// type liteEngineNomadClient struct {
// 	Address string
// 	NodeID  string
// 	BaseURL string
// 	Client  *nomad.Client
// }

// func (l *liteEngineNomadClient) Setup(ctx context.Context, in *api.SetupRequest) (*api.SetupResponse, error) {
// 	b, err := json.Marshal(in)
// 	if err != nil {
// 		return nil, err
// 	}
// 	constraint := &nomad.Constraint{
// 		LTarget: "${node.unique.id}",
// 		RTarget: l.NodeID,
// 		Operand: "=",
// 	}
// 	job := &nomad.Job{
// 		ID:          stringToPtr(random(20)),
// 		Name:        stringToPtr(random(20)),
// 		Type:        stringToPtr("batch"),
// 		Datacenters: []string{"dc1"},
// 		Constraints: []*nomad.Constraint{
// 			constraint,
// 		},
// 		TaskGroups: []*nomad.TaskGroup{
// 			{
// 				Name:  stringToPtr("setup_vm"),
// 				Count: intToPtr(1),
// 				Tasks: []*nomad.Task{
// 					{
// 						Name:   "ignite_setup",
// 						Driver: "raw_exec",
// 						Config: map[string]interface{}{
// 							"command": "/usr/bin/curl",
// 							"args":    []string{"-H", "Content-Type: application/json", "-X", "POST", fmt.Sprintf("%s/setup", l.BaseURL), "-d", string(b)},
// 						},
// 					},
// 				},
// 			},
// 		}}
// 	_, _, err = l.Client.Jobs().Register(job, nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	pollForJob(*job.ID, l.Client)
// 	return &api.SetupResponse{}, nil
// }

// func (l *liteEngineNomadClient) Destroy(ctx context.Context, in *api.DestroyRequest) (*api.DestroyResponse, error) {
// 	return &api.DestroyResponse{}, nil
// }

// func (l *liteEngineNomadClient) StartStep(ctx context.Context, in *api.StartStepRequest) (*api.StartStepResponse, error) {
// 	b, err := json.Marshal(in)
// 	if err != nil {
// 		return nil, err
// 	}
// 	constraint := &nomad.Constraint{
// 		LTarget: "${node.unique.id}",
// 		RTarget: l.NodeID,
// 		Operand: "=",
// 	}
// 	job := &nomad.Job{
// 		ID:          stringToPtr(random(20)),
// 		Name:        stringToPtr(random(20)),
// 		Type:        stringToPtr("batch"),
// 		Datacenters: []string{"dc1"},
// 		Constraints: []*nomad.Constraint{
// 			constraint,
// 		},
// 		TaskGroups: []*nomad.TaskGroup{
// 			{
// 				Name:  stringToPtr("delete_vm"),
// 				Count: intToPtr(1),
// 				Tasks: []*nomad.Task{
// 					{
// 						Name:   "ignite_delete",
// 						Driver: "raw_exec",
// 						Config: map[string]interface{}{
// 							"command": "/usr/bin/curl",
// 							"args":    []string{"-H", "Content-Type: application/json", "-X", "POST", fmt.Sprintf("%s/start_step", l.BaseURL), "-d", string(b)},
// 						},
// 					},
// 				},
// 			},
// 		}}
// 	_, _, err = l.Client.Jobs().Register(job, nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	pollForJob(*job.ID, l.Client)
// 	return &api.StartStepResponse{}, nil
// }

// func (l *liteEngineNomadClient) PollStep(ctx context.Context, in *api.PollStepRequest) (*api.PollStepResponse, error) {
// 	b, err := json.Marshal(in)
// 	if err != nil {
// 		return nil, err
// 	}
// 	constraint := &nomad.Constraint{
// 		LTarget: "${node.unique.id}",
// 		RTarget: l.NodeID,
// 		Operand: "=",
// 	}
// 	job := &nomad.Job{
// 		ID:          stringToPtr(random(20)),
// 		Name:        stringToPtr(random(20)),
// 		Type:        stringToPtr("batch"),
// 		Datacenters: []string{"dc1"},
// 		Constraints: []*nomad.Constraint{
// 			constraint,
// 		},
// 		TaskGroups: []*nomad.TaskGroup{
// 			{
// 				Name:  stringToPtr("poll_vm"),
// 				Count: intToPtr(1),
// 				Tasks: []*nomad.Task{
// 					{
// 						Name:   "ignite_poll",
// 						Driver: "raw_exec",
// 						Config: map[string]interface{}{
// 							"command": "/usr/bin/curl",
// 							"args":    []string{"-H", "Content-Type: application/json", "-X", "POST", fmt.Sprintf("%s/poll_step", l.BaseURL), "-d", string(b)},
// 						},
// 					},
// 				},
// 			},
// 		}}
// 	_, _, err = l.Client.Jobs().Register(job, nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	pollForJob(*job.ID, l.Client)
// 	return &api.PollStepResponse{ExitCode: 0, Exited: true}, nil
// }

// func (l *liteEngineNomadClient) RetryPollStep(ctx context.Context, in *api.PollStepRequest, timeout time.Duration) (step *api.PollStepResponse, pollError error) {
// 	startTime := time.Now()
// 	retryCtx, cancel := context.WithTimeout(ctx, timeout)
// 	defer cancel()
// 	for i := 0; ; i++ {
// 		select {
// 		case <-retryCtx.Done():
// 			return step, retryCtx.Err()
// 		default:
// 		}
// 		step, pollError = l.PollStep(retryCtx, in)
// 		fmt.Printf("step: %+v\n", step)
// 		fmt.Printf("pollError: %s\n", pollError)
// 		if pollError == nil {
// 			logger.FromContext(retryCtx).
// 				WithField("duration", time.Since(startTime)).
// 				Trace("RetryPollStep: step completed")
// 			return step, pollError
// 		}
// 		time.Sleep(time.Millisecond * 10) //nolint:gomnd
// 	}
// }

// func (l *liteEngineNomadClient) GetStepLogOutput(ctx context.Context, in *api.StreamOutputRequest, w io.Writer) error {
// 	return nil
// }

// func (l *liteEngineNomadClient) Health(ctx context.Context) (*api.HealthResponse, error) {
// 	time.Sleep(2 * time.Second)
// 	return &api.HealthResponse{OK: true}, nil
// }

// func (l *liteEngineNomadClient) RetryHealth(ctx context.Context, timeout time.Duration) (*api.HealthResponse, error) {
// 	return l.Health(ctx)
// }

// // Helper function to convert an int to a pointer to an int
// func intToPtr(i int) *int {
// 	return &i
// }

// var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

// func random(n int) string {
// 	b := make([]rune, n)
// 	for i := range b {
// 		b[i] = letterRunes[rand.Intn(len(letterRunes))]
// 	}
// 	return string(b)
// }

// func init() {
// 	rand.Seed(time.Now().UnixNano())
// }

// // Helper function to convert an int to a pointer to an int
// func stringToPtr(i string) *string {
// 	return &i
// }

// func pollForJob(id string, client *nomad.Client) *nomad.Job {
// 	var job *nomad.Job
// 	// Poll for the response
// 	for {
// 		// Get the job status
// 		job, _, err := client.Jobs().Info(id, nil)
// 		if err != nil {
// 			fmt.Println("error: ", err)
// 			log.Fatal(err)
// 		}
// 		fmt.Printf("job is %+v", job)

// 		// Check the job status
// 		if *job.Status == "running" {
// 			fmt.Println("Job is running")
// 		} else if *job.Status == "pending" {
// 			fmt.Println("job is pending")
// 		} else if *job.Status == "failed" {
// 			fmt.Println("Job failed")
// 			break
// 		} else if *job.Status == "dead" {
// 			fmt.Println("Job is dead")
// 			break
// 		}
// 	}
// 	return job
// }
