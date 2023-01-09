package tester

import (
	"context"
	"net/http"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/google/uuid"
	"github.com/harness/lite-engine/api"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

type command struct {
	envFile string
	pool    string
	scale   int64
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
		Int64Var(&c.scale)
}

func (c *command) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envFile)
	if envError != nil {
		logrus.WithError(envError).
			Warnf("delegate: failed to load environment variables from file: %s", c.envFile)
	}

	return c.runPipeline()
}

func (c *command) runPipeline() error {
	client := &HTTPClient{
		Client:   &http.Client{Timeout: time.Duration(1000) * time.Second},
		Endpoint: "http://127.0.0.1:3000",
	}
	ctx := context.Background()

	id := uuid.NewString()
	mount := false
	// setup the stage
	setupIn := &harness.SetupVMRequest{
		ID:     id,
		PoolID: c.pool,
		SetupRequest: api.SetupRequest{
			MountDockerSocket: &mount,
		},
	}
	if _, err := client.Setup(ctx, setupIn); err != nil {
		return errors.Wrap(err, "vm setup failed")
	}

	// run a command on host
	runIn := &harness.ExecuteVMRequest{
		StageRuntimeID: id,
		CorrelationID:  id,
		PoolID:         c.pool,
		StartStepRequest: api.StartStepRequest{
			ID: uuid.NewString(),
			Run: api.RunConfig{
				Command:    []string{"sleep 5"},
				Entrypoint: []string{"bash", "-c"},
			},
		},
	}
	if _, err := client.Step(ctx, runIn); err != nil {
		return errors.Wrap(err, "execute step failed")
	}

	// cleanup
	cleanupIn := &CleanupRequest{
		PoolID: c.pool,
		ID:     id,
	}
	if err := client.Destroy(ctx, cleanupIn); err != nil {
		return errors.Wrap(err, "vm clean failed")
	}
	return nil
}
