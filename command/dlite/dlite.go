// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package dlite

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/poolfile"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/signal"
	leapi "github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"
	lelivelog "github.com/harness/lite-engine/livelog"
	lestream "github.com/harness/lite-engine/logstream/remote"
	"github.com/wings-software/dlite/delegate"
	"github.com/wings-software/dlite/poller"
	"github.com/wings-software/dlite/router"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

type dliteCommand struct {
	envFile         string
	env             config.EnvConfig
	poolFile        string
	poolManager     *drivers.Manager
	stageOwnerStore store.StageOwnerStore
	runnerName      string
	liteEnginePath  string
}

var (
	taskInterval  = 3 * time.Second
	taskExecutors = 100
)

func RegisterDlite(app *kingpin.Application) {
	c := new(dliteCommand)

	c.poolManager = &drivers.Manager{}

	cmd := app.Command("dlite", "starts the runner with polling enabled for accepting tasks").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		StringVar(&c.envFile)
	cmd.Flag("pool", "file to seed the pool").
		StringVar(&c.poolFile)
}

// helper function configures the global logger from
// the loaded configuration.
func setupLogger(c *config.EnvConfig) {
	logger.Default = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)

	if c.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}
	if c.Trace {
		logrus.SetLevel(logrus.TraceLevel)
	}
}

func (c *dliteCommand) startPoller(ctx context.Context, tags []string) error {
	router := router.NewRouter(routeMap(c))
	// TODO (Vistaar): Set a token updater thread which resets the token. Right now it's set to 10 hours
	token, err := delegate.Token("audience", "issuer", c.env.Dlite.AccountID, c.env.Dlite.AccountSecret, 10*time.Hour)
	if err != nil {
		return err
	}
	// Client to interact with the harness server
	client := delegate.New(c.env.Dlite.ManagerEndpoint, c.env.Dlite.AccountID, token, true)
	poller := poller.New(c.env.Dlite.AccountID, c.env.Dlite.AccountSecret, c.env.Dlite.Name, client, router)
	err = poller.Register(ctx, tags, taskInterval)
	if err != nil {
		return err
	}
	poller.Poll(ctx, taskExecutors, taskInterval)
	return nil
}

func (c *dliteCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envFile)
	if envError != nil {
		logrus.WithError(envError).
			Warnf("dlite: failed to load environment variables from file: %s", c.envFile)
	}
	// load the configuration from the environment
	env, err := config.FromEnviron()
	if err != nil {
		return err
	}
	c.env = env
	// setup the global logrus logger.
	setupLogger(&env)
	db, err := database.ProvideDatabase(env.Database.Driver, env.Database.Datasource)
	if err != nil {
		logrus.WithError(err).
			Fatalln("Unable to start the database")
	}

	// Set runner name & lite engine path
	c.liteEnginePath = env.Settings.LiteEnginePath
	c.runnerName = env.Runner.Name

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	instanceStore := database.ProvideInstanceStore(db)
	c.stageOwnerStore = database.NewStageOwnerStore(db)
	c.poolManager = drivers.New(ctx, instanceStore, c.liteEnginePath, c.runnerName)

	configPool, confErr := poolfile.ConfigPoolFile(c.poolFile, &env)
	if confErr != nil {
		logrus.WithError(confErr).
			Fatalln("Unable to load pool file, or use an in memory pool")
	}

	pools, err := poolfile.ProcessPool(configPool, c.runnerName)
	if err != nil {
		logrus.WithError(err).
			Errorln("dlite: unable to process pool file")
		return err
	}

	err = c.poolManager.Add(pools...)
	if err != nil {
		logrus.WithError(err).
			Errorln("dlite: unable to add pools")
		return err
	}

	err = c.poolManager.PingDriver(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("dlite: unable to ping driver")
		return err
	}

	// setup lifetimes of instances
	busyMaxAge := time.Hour * time.Duration(env.Settings.BusyMaxAge) // includes time required to setup an instance
	freeMaxAge := time.Hour * time.Duration(env.Settings.FreeMaxAge)
	err = c.poolManager.StartInstancePurger(ctx, busyMaxAge, freeMaxAge)
	if err != nil {
		logrus.WithError(err).
			Errorln("dlite: failed to start instance purger")
		return err
	}

	// lets remove any old instances.
	if !env.Settings.ReusePool {
		cleanErr := c.poolManager.CleanPools(ctx, true, true)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("dlite: unable to clean pools")
		} else {
			logrus.Infoln("dlite: pools cleaned")
		}
	}
	// seed pools
	buildPoolErr := c.poolManager.BuildPools(ctx)
	if buildPoolErr != nil {
		logrus.WithError(buildPoolErr).
			Errorln("dlite: unable to build pool")
		return buildPoolErr
	}
	logrus.Infoln("dlite: pool created")

	hook := loghistory.New()
	logrus.AddHook(hook)

	// TODO (Vistaar): Add support for tags based on available pools
	err = c.startPoller(ctx, []string{})
	if err != nil {
		return err
	}

	// lets remove any old instances.
	if !env.Settings.ReusePool {
		cleanErr := c.poolManager.CleanPools(context.Background(), true, true)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("dlite: unable to clean pools")
		} else {
			logrus.Infoln("dlite: pools cleaned")
		}
	}

	return nil
}

func (c *dliteCommand) handleSetup(ctx context.Context, reqData *SetupVmRequest) (*SetupVmResponse, error) {
	if reqData.ID == "" {
		return nil, errors.New("mandatory field 'id' in the request body is empty")
	}

	if reqData.PoolID == "" {
		return nil, errors.New("mandatory field 'pool_id' in the request body is empty")
	}

	if err := c.stageOwnerStore.Create(ctx, &types.StageOwner{StageID: reqData.ID, PoolName: reqData.PoolID}); err != nil {
		logrus.WithError(err).Errorln("failed to create stage owner entity")
		return nil, err
	}

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	var logr *logrus.Entry
	if reqData.SetupRequest.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "dlite:setup").
			WithField("pool", reqData.PoolID).
			WithField("correlationID", reqData.CorrelationID)
	} else {
		wc := getStreamLogger(reqData.SetupRequest.LogConfig, reqData.LogKey, reqData.CorrelationID)
		defer func() {
			if err := wc.Close(); err != nil {
				logrus.WithError(err).Debugln("failed to close log stream")
			}
		}()

		log.Out = wc
		log.SetLevel(logrus.TraceLevel)
		logr = log.WithField("pool", reqData.PoolID)

		ctx = logger.WithContext(ctx, logger.Logrus(logr))
	}

	// append global volumes to the setup request.
	for _, pair := range c.env.Runner.Volumes {
		src, _, ro, err := resource.ParseVolume(pair)
		if err != nil {
			log.Warn(err)
			continue
		}
		vol := lespec.Volume{
			HostPath: &lespec.VolumeHostPath{
				ID:       id(src),
				Name:     id(src),
				Path:     src,
				ReadOnly: ro,
			},
		}
		reqData.Volumes = append(reqData.Volumes, &vol)
	}

	poolName := reqData.PoolID
	if !c.poolManager.Exists(poolName) {
		return nil, errors.New("pool not defined")
	}

	instance, err := c.poolManager.Provision(ctx, poolName, c.runnerName, c.liteEnginePath)
	if err != nil {
		return nil, errors.New("failed provisioning")
	}

	if instance.IsHibernated {
		instance, err = c.poolManager.StartInstance(ctx, poolName, instance.ID)
		if err != nil {
			return nil, errors.New("failed to start the instance up")
		}
	}

	logr = logr.
		WithField("ip", instance.Address).
		WithField("id", instance.ID)

	// cleanUpFn is a function to terminate the instance if an error occurs later in the handleSetup function
	cleanUpFn := func() {
		errCleanUp := c.poolManager.Destroy(context.Background(), poolName, instance.ID)
		if errCleanUp != nil {
			logr.WithError(errCleanUp).Errorln("failed to delete failed instance client")
		}
	}

	instance.Stage = reqData.ID
	instance.Updated = time.Now().Unix()
	err = c.poolManager.Update(ctx, instance)
	if err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to tag: %w", err)
	}

	client, err := lehelper.GetClient(instance, c.runnerName)
	if err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to create LE client: %w", err)
	}

	const timeoutSetup = 20 * time.Minute // TODO: Move to configuration

	// try the healthcheck api on the lite-engine until it responds ok
	logr.Traceln("running healthcheck and waiting for an ok response")
	if _, err = client.RetryHealth(ctx, timeoutSetup); err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to call lite-engine retry health: %w", err)
	}

	logr.Traceln("retry health check complete")

	setupResponse, err := client.Setup(ctx, &reqData.SetupRequest)
	if err != nil {
		go cleanUpFn()
		return nil, fmt.Errorf("failed to call setup lite-engine: %w", err)
	}

	logr.WithField("response", fmt.Sprintf("%+v", setupResponse)).
		Traceln("VM setup is complete")

	return &SetupVmResponse{InstanceID: instance.ID, IPAddress: instance.Address}, nil
}

func (c *dliteCommand) handleStep(ctx context.Context, reqData *ExecuteVmRequest) (*leapi.PollStepResponse, error) {
	if reqData.ID == "" && reqData.IPAddress == "" {
		return nil, fmt.Errorf("either parameter 'id' or 'ip_address' must be provided")
	}

	if reqData.PoolID == "" {
		return nil, fmt.Errorf("mandatory field 'pool_id' in the request body is empty")
	}

	logr := logrus.
		WithField("api", "dlite:step").
		WithField("step_id", reqData.StartStepRequest.ID).
		WithField("pool", reqData.PoolID).
		WithField("correlation_id", reqData.CorrelationID)

	// add global volumes as mounts only if image is specified
	if reqData.Image != "" {
		for _, pair := range c.env.Runner.Volumes {
			src, dest, _, err := resource.ParseVolume(pair)
			if err != nil {
				logr.Warn(err)
				continue
			}
			mount := &lespec.VolumeMount{
				Name: id(src),
				Path: dest,
			}
			reqData.Volumes = append(reqData.Volumes, mount)
		}
	}
	inst, err := c.poolManager.GetInstanceByStageID(ctx, reqData.PoolID, reqData.ID)
	if err != nil {
		return nil, fmt.Errorf("cannot get the instance by stageId: %w", err)
	}

	logr = logr.
		WithField("ip", inst.Address)

	client, err := lehelper.GetClient(inst, c.runnerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	logr.Traceln("running StartStep")

	startStepResponse, err := client.StartStep(ctx, &reqData.StartStepRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to call LE.StartStep: %w", err)
	}

	logr.WithField("startStepResponse", startStepResponse).
		Traceln("LE.StartStep complete")

	const timeoutStep = 4 * time.Hour // TODO: Move to configuration

	pollResponse, err := client.RetryPollStep(ctx, &leapi.PollStepRequest{ID: reqData.StartStepRequest.ID}, timeoutStep)
	if err != nil {
		return nil, fmt.Errorf("failed to call LE.RetryPollStep: %w", err)
	}

	logr.WithField("pollResponse", pollResponse).
		Traceln("LE.RetryPollStep complete")

	return pollResponse, nil
}

func (c *dliteCommand) handleDestroy(ctx context.Context, reqData *VmCleanupRequest) error {
	if reqData.PoolID == "" {
		return fmt.Errorf("mandatory field 'pool_id' in the request body is empty")
	}

	if reqData.StageRuntimeID == "" {
		return fmt.Errorf("mandatory field 'stage_runtime_id' in the request body is empty")
	}

	logr := logrus.
		WithField("api", "dlite:destroy").
		WithField("stage_runtime_id", reqData.StageRuntimeID).
		WithField("pool", reqData.PoolID)

	var instanceID string

	inst, err := c.poolManager.GetInstanceByStageID(ctx, reqData.PoolID, reqData.StageRuntimeID)
	if err != nil {
		return fmt.Errorf("cannot get the instance by tag: %w", err)
	}
	if inst == nil {
		return fmt.Errorf("instance with provided ID not found")
	}

	instanceID = inst.ID

	logr = logr.
		WithField("instance_id", instanceID)

	if err := c.poolManager.Destroy(ctx, reqData.PoolID, instanceID); err != nil {
		return fmt.Errorf("annot destroy the instance: %w", err)
	}
	logr.Traceln("destroyed instance")

	if err := c.stageOwnerStore.Delete(ctx, reqData.StageRuntimeID); err != nil {
		logrus.WithError(err).Errorln("failed to delete stage owner entity")
	}
	return nil
}

func getStreamLogger(cfg leapi.LogConfig, logKey, correlationID string) *lelivelog.Writer {
	client := lestream.NewHTTPClient(cfg.URL, cfg.AccountID,
		cfg.Token, cfg.IndirectUpload, false)
	wc := lelivelog.New(client, logKey, correlationID, nil)
	go func() {
		if err := wc.Open(); err != nil {
			logrus.WithError(err).Debugln("failed to open log stream")
		}
	}()
	return wc
}

// generate a id from the filename
// /path/to/a.txt and /other/path/to/a.txt should generate different hashes
// eg - a-txt10098 and a-txt-270089
func id(filename string) string {
	h := fnv.New32a()
	h.Write([]byte(filename))
	return strings.Replace(filepath.Base(filename), ".", "-", -1) + strconv.Itoa(int(h.Sum32()))
}
