package tester

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/google/uuid"
	"github.com/harness/lite-engine/api"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	clientTimeoutSecs = 1000
)

type command struct {
	envFile string
	pool    string
	scale   int
	loop    int
}

var netClient = &HTTPClient{}

func init() { //nolint:gochecknoinits
	tr := &http.Transport{
		MaxIdleConns:        2000,
		MaxIdleConnsPerHost: 2000,
	}
	netClient = &HTTPClient{
		Client: &http.Client{
			Transport: tr,
			Timeout:   time.Duration(clientTimeoutSecs) * time.Second},
		Endpoint: "http://127.0.0.1:3000",
	}
}

func Register(app *kingpin.Application) {
	c := new(command)

	cmd := app.Command("tester", "starts the delegate runner testing").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		StringVar(&c.envFile)
	cmd.Flag("pool", "pool name").
		StringVar(&c.pool)
	cmd.Flag("scale", "number of parallel builds to run").
		IntVar(&c.scale)
	cmd.Flag("loop", "number of times to run the test").
		IntVar(&c.loop)
}

func (c *command) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envFile)
	if envError != nil {
		logrus.WithError(envError).
			Warnf("delegate: failed to load environment variables from file: %s", c.envFile)
	}

	if c.loop == 0 {
		c.loop = 1
	}

	for i := 0; i < c.loop; i++ {
		if err := c.scaleTest(i); err != nil {
			return err
		}
	}
	return nil
}

func (c *command) scaleTest(loopIdx int) error {
	var wg sync.WaitGroup
	fail := false
	for i := 0; i < c.scale; i++ {
		wg.Add(1)

		go func(i int) {
			id := fmt.Sprint(i)
			if err := c.runPipeline(id); err != nil {
				fail = true
				logrus.WithError(err).WithField("id", id).Infoln("pipeline run failed")
			}
			wg.Done()
		}(i + c.scale*loopIdx)
	}
	wg.Wait()

	if fail {
		return fmt.Errorf("scale test failed")
	}
	return nil
}

func (c *command) runPipeline(id string) error {
	client := netClient
	ctx := context.Background()

	mount := false
	// setup the stage
	setupIn := &harness.SetupVMRequest{
		ID:     id,
		PoolID: c.pool,
		SetupRequest: api.SetupRequest{
			MountDockerSocket: &mount,
		},
	}
	logrus.WithField("id", id).Infoln("Starting vm setup")
	if _, err := client.Setup(ctx, setupIn); err != nil {
		return errors.Wrap(err, "vm setup failed")
	}
	logrus.WithField("id", id).Infoln("Completed vm setup")

	// run a command on host
	runIn := &harness.ExecuteVMRequest{
		StageRuntimeID: id,
		CorrelationID:  id,
		StartStepRequest: api.StartStepRequest{
			ID: uuid.NewString(),
			Run: api.RunConfig{
				Command:    []string{"sleep 5"},
				Entrypoint: []string{"bash", "-c"},
			},
		},
	}
	logrus.WithField("id", id).Infoln("Starting execute step")
	if _, err := client.Step(ctx, runIn); err != nil {
		return errors.Wrap(err, "execute step failed")
	}
	logrus.WithField("id", id).Infoln("Completed execute step")

	// cleanup
	cleanupIn := &CleanupRequest{
		PoolID: c.pool,
		ID:     id,
	}

	logrus.WithField("id", id).Infoln("Starting vm cleanup")
	if err := client.Destroy(ctx, cleanupIn); err != nil {
		return errors.Wrap(err, "vm clean failed")
	}
	logrus.WithField("id", id).Infoln("Completed vm cleanup")

	return nil
}
