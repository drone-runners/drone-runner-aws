package delegate

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/httprender"
	errors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/server"
	"github.com/drone/signal"
	"github.com/go-chi/chi/v5"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/httphelper"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

type delegateCommand struct {
	envFile         string
	env             config.EnvConfig
	poolFile        string
	poolManager     *drivers.Manager
	stageOwnerStore store.StageOwnerStore
}

func (c *delegateCommand) delegateListener() http.Handler {
	mux := chi.NewMux()

	mux.Use(harness.Middleware)

	mux.Post("/pool_owner", c.handlePoolOwner)
	mux.Post("/setup", c.handleSetup)
	mux.Post("/destroy", c.handleDestroy)
	mux.Post("/step", c.handleStep)

	return mux
}

func RegisterDelegate(app *kingpin.Application) {
	c := new(delegateCommand)

	c.poolManager = &drivers.Manager{}

	cmd := app.Command("delegate", "starts the delegate").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		StringVar(&c.envFile)
	cmd.Flag("pool", "file to seed the pool").
		StringVar(&c.poolFile)
}

func (c *delegateCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envFile)
	if envError != nil {
		logrus.WithError(envError).
			Warnf("delegate: failed to load environment variables from file: %s", c.envFile)
	}
	// load the configuration from the environment
	env, err := config.FromEnviron()
	if err != nil {
		return err
	}
	if env.Settings.HarnessTestBinaryURI == "" {
		env.Settings.HarnessTestBinaryURI = "https://app.harness.io/storage/harness-download/harness-ti/split_tests"
	}
	c.env = env
	// setup the global logrus logger.
	harness.SetupLogger(&c.env)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	instanceStore, stageOwnerStore, err := database.ProvideStore(c.env.Database.Driver, c.env.Database.Datasource)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
	}

	c.stageOwnerStore = stageOwnerStore
	c.poolManager = drivers.New(ctx, instanceStore, &c.env)

	_, err = harness.SetupPool(ctx, &c.env, c.poolManager, c.poolFile)
	defer harness.Cleanup(&c.env, c.poolManager) //nolint: errcheck
	if err != nil {
		return err
	}

	hook := loghistory.New()
	logrus.AddHook(hook)

	var g errgroup.Group
	runnerServer := server.Server{
		Addr:    c.env.Server.Port,
		Handler: c.delegateListener(),
	}

	logrus.WithField("addr", runnerServer.Addr).
		WithField("kind", resource.Kind).
		WithField("type", resource.Type).
		Infoln("starting the server")

	g.Go(func() error {
		<-ctx.Done()
		return harness.Cleanup(&c.env, c.poolManager)
	})

	g.Go(func() error {
		return runnerServer.ListenAndServe(ctx)
	})

	waitErr := g.Wait()
	if waitErr != nil {
		logrus.WithError(waitErr).
			Errorln("shutting down the server")
	}
	return waitErr
}

func (c *delegateCommand) handlePoolOwner(w http.ResponseWriter, r *http.Request) {
	poolName := r.URL.Query().Get("pool")
	if poolName == "" {
		httprender.BadRequest(w, "mandatory URL parameter 'pool' is missing", nil)
		return
	}

	type poolOwnerResponse struct {
		Owner bool `json:"owner"`
	}

	if !c.poolManager.Exists(poolName) {
		httprender.OK(w, poolOwnerResponse{Owner: false})
		return
	}

	stageID := r.URL.Query().Get("stageId")
	if stageID != "" {
		entity, err := c.stageOwnerStore.Find(context.Background(), stageID)
		if err != nil {
			logrus.WithError(err).WithField("pool", poolName).WithField("stageId", stageID).Error("failed to find the stage in store")
			httprender.OK(w, poolOwnerResponse{Owner: false})
			return
		}

		if entity.PoolName != poolName {
			logrus.WithError(err).WithField("pool", poolName).WithField("stageId", stageID).Errorf("found stage with different pool: %s", entity.PoolName)
			httprender.OK(w, poolOwnerResponse{Owner: false})
			return
		}
	}

	httprender.OK(w, poolOwnerResponse{Owner: true})
}

func (c *delegateCommand) handleSetup(w http.ResponseWriter, r *http.Request) {
	req := &harness.SetupVMRequest{}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		logrus.WithError(err).Error("could not decode request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}
	ctx := r.Context()
	resp, err := harness.HandleSetup(ctx, req, c.stageOwnerStore, &c.env, c.poolManager)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.ID).WithError(err).Error("could not setup VM")
		writeError(w, err)
		return
	}
	httprender.OK(w, resp)
}

func (c *delegateCommand) handleStep(w http.ResponseWriter, r *http.Request) {
	req := &harness.ExecuteVMRequest{}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		logrus.WithError(err).Error("could not decode VM step execute request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}
	ctx := r.Context()
	resp, err := harness.HandleStep(ctx, req, c.stageOwnerStore, &c.env, c.poolManager)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.StageRuntimeID).WithField("step_id", req.ID).
			WithError(err).Error("could not execute step on VM")
		writeError(w, err)
		return
	}
	httprender.OK(w, resp)
}

func (c *delegateCommand) handleDestroy(w http.ResponseWriter, r *http.Request) {
	// TODO: Change the java object to match VmCleanupRequest
	rs := &struct {
		ID            string `json:"id"`
		InstanceID    string `json:"instance_id"`
		PoolID        string `json:"pool_id"`
		CorrelationID string `json:"correlation_id"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(rs); err != nil {
		logrus.WithError(err).Error("could not decode VM destroy request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}
	req := &harness.VMCleanupRequest{PoolID: rs.PoolID, StageRuntimeID: rs.ID}
	ctx := r.Context()
	err := harness.HandleDestroy(ctx, req, c.stageOwnerStore, &c.env, c.poolManager)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.StageRuntimeID).WithError(err).Error("could not destroy VM")
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeError(w http.ResponseWriter, err error) {
	switch err.(type) {
	case *errors.BadRequestError:
		httphelper.WriteBadRequest(w, err)
	case *errors.NotFoundError:
		httphelper.WriteNotFound(w, err)
	default:
		httphelper.WriteInternalError(w, err)
	}
}
