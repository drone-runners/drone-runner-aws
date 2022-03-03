// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package delegate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/httprender"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/google"

	"github.com/drone/runner-go/logger"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/server"
	"github.com/drone/signal"
	leapi "github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
	lelivelog "github.com/harness/lite-engine/livelog"
	lestream "github.com/harness/lite-engine/logstream/remote"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

type delegateCommand struct {
	envfile             string
	awsPoolfile         string
	googlePoolFile      string
	defaultPoolSettings vmpool.DefaultSettings
	poolManager         *vmpool.Manager
}

const TagStageID = vmpool.TagPrefix + "stage-id"

// helper function configures the global logger from
// the loaded configuration.
func setupLogger(config *Config) {
	logger.Default = logger.Logrus(
		logrus.NewEntry(
			logrus.StandardLogger(),
		),
	)

	if config.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}
	if config.Trace {
		logrus.SetLevel(logrus.TraceLevel)
	}
}

func (c *delegateCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envfile)
	if envError != nil {
		logrus.WithError(envError).
			Errorln("delegate: failed to load environment variables")
	}
	// load the configuration from the environment
	var config Config
	processEnvErr := envconfig.Process("", &config)
	if processEnvErr != nil {
		logrus.WithError(processEnvErr).
			Errorln("delegate: failed to load configuration")
	}
	// load the configuration from the environment
	config, err := fromEnviron()
	if err != nil {
		return err
	}
	// setup the global logrus logger.
	setupLogger(&config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})
	// generate cert files if needed
	certGenerationErr := le.GenerateLECerts(config.Runner.Name, config.DefaultPoolSettings.CertificateFolder)
	if certGenerationErr != nil {
		logrus.WithError(certGenerationErr).
			Errorln("delegate: failed to generate certificates")
		return certGenerationErr
	}
	// read cert files into memory
	var readCertsErr error
	config.DefaultPoolSettings.CaCertFile, config.DefaultPoolSettings.CertFile, config.DefaultPoolSettings.KeyFile, readCertsErr = le.ReadLECerts(config.DefaultPoolSettings.CertificateFolder)
	if readCertsErr != nil {
		logrus.WithError(readCertsErr).
			Errorln("delegate: failed to read certificates")
		return readCertsErr
	}
	// we have enough information for default pool settings
	c.defaultPoolSettings = vmpool.DefaultSettings{
		RunnerName:          config.Runner.Name,
		AwsAccessKeyID:      config.DefaultPoolSettings.AwsAccessKeyID,
		AwsAccessKeySecret:  config.DefaultPoolSettings.AwsAccessKeySecret,
		AwsRegion:           config.DefaultPoolSettings.AwsRegion,
		AwsAvailabilityZone: config.DefaultPoolSettings.AwsAvailabilityZone,
		AwsKeyPairName:      config.DefaultPoolSettings.AwsKeyPairName,
		LiteEnginePath:      config.DefaultPoolSettings.LiteEnginePath,
		CaCertFile:          config.DefaultPoolSettings.CaCertFile,
		CertFile:            config.DefaultPoolSettings.CertFile,
		KeyFile:             config.DefaultPoolSettings.KeyFile,
	}
	// process the pool file
	poolsAWS, err := cloudaws.ProcessPoolFile(c.awsPoolfile, &c.defaultPoolSettings)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: unable to parse aws pool file")
		os.Exit(1) //nolint:gocritic // failing fast before we do any work.
	}
	err = c.poolManager.Add(poolsAWS...)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: unable to add to aws pools")
		os.Exit(1)
	}
	poolsGCP, err := google.ProcessPoolFile(c.googlePoolFile, &c.defaultPoolSettings)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: unable to parse google pool file")
		os.Exit(1)
	}

	err = c.poolManager.Add(poolsGCP...)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: unable to add to google pools")
		os.Exit(1)
	}

	err = c.poolManager.Ping(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: cannot connect to cloud provider")
		return err
	}

	// setup lifetimes of instances
	busyMaxAge := time.Hour * time.Duration(config.DefaultPoolSettings.BusyMaxAge) // includes time required to setup an instance
	freeMaxAge := time.Hour * time.Duration(config.DefaultPoolSettings.FreeMaxAge)
	err = c.poolManager.StartInstancePurger(ctx, busyMaxAge, freeMaxAge)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: failed to start instance purger")
		return err
	}

	// lets remove any old instances.
	if !config.DefaultPoolSettings.ReusePool {
		cleanErr := c.poolManager.CleanPools(ctx, true, true)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("delegate: unable to clean pools")
		} else {
			logrus.Infoln("delegate: pools cleaned")
		}
	}
	// seed pools
	buildPoolErr := c.poolManager.BuildPools(ctx)
	if buildPoolErr != nil {
		logrus.WithError(buildPoolErr).
			Errorln("delegate: unable to build pool")
		return buildPoolErr
	}
	logrus.Infoln("delegate: pool created")

	hook := loghistory.New()
	logrus.AddHook(hook)

	var g errgroup.Group
	runnerServer := server.Server{
		Addr:    config.Server.Port,
		Handler: c.delegateListener(),
	}

	logrus.WithField("addr", runnerServer.Addr).
		WithField("kind", resource.Kind).
		WithField("type", resource.Type).
		Infoln("starting the server")

	g.Go(func() error {
		return runnerServer.ListenAndServe(ctx)
	})

	waitErr := g.Wait()
	if waitErr != nil {
		logrus.WithError(waitErr).
			Errorln("shutting down the server")
	}

	// lets remove any old instances.
	if !config.DefaultPoolSettings.ReusePool {
		cleanErr := c.poolManager.CleanPools(context.Background(), true, true)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("delegate: unable to clean pools")
		} else {
			logrus.Infoln("delegate: pools cleaned")
		}
	}

	return waitErr
}

func (c *delegateCommand) delegateListener() http.Handler {
	mux := chi.NewMux()

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrap := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			reqStart := time.Now().UTC()
			next.ServeHTTP(wrap, r)

			status := wrap.Status()
			dur := time.Since(reqStart).Milliseconds()

			logr := logrus.WithContext(r.Context()).
				WithField("t", reqStart.Format(time.RFC3339)).
				WithField("status", status).
				WithField("dur[ms]", dur)
			logLine := "HTTP: " + r.Method + " " + r.URL.RequestURI()
			if status >= http.StatusInternalServerError {
				logr.Errorln(logLine)
			} else {
				logr.Infoln(logLine)
			}
		})
	})

	mux.Post("/pool_owner", c.handlePoolOwner)
	mux.Post("/setup", c.handleSetup)
	mux.Post("/destroy", c.handleDestroy)
	mux.Post("/step", c.handleStep)

	return mux
}

func (c *delegateCommand) handlePoolOwner(w http.ResponseWriter, r *http.Request) {
	poolName := r.URL.Query().Get("pool")
	if poolName == "" {
		httprender.BadRequest(w, "mandatory URL parameter 'pool' is missing", nil)
		return
	}

	httprender.OK(w, struct {
		Owner bool `json:"owner"`
	}{
		Owner: c.poolManager.Exists(poolName),
	})
}

func (c *delegateCommand) handleSetup(w http.ResponseWriter, r *http.Request) {
	reqData := &struct {
		ID                 string            `json:"id"`
		PoolID             string            `json:"pool_id"`
		Tags               map[string]string `json:"tags"`
		CorrelationID      string            `json:"correlation_id"`
		LogKey             string            `json:"log_key"`
		leapi.SetupRequest `json:"setup_request"`
	}{}

	if err := getJSONDataFromReader(r.Body, reqData); err != nil {
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	if reqData.ID == "" {
		httprender.BadRequest(w, "mandatory field 'id' in the request body is empty", nil)
		return
	}

	if reqData.PoolID == "" {
		httprender.BadRequest(w, "mandatory field 'pool_id' in the request body is empty", nil)
		return
	}

	ctx := r.Context()

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	var logr *logrus.Entry
	if reqData.SetupRequest.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "delegate:setup").
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

		ctx = logger.WithContext(r.Context(), logger.Logrus(logr))
	}

	poolName := reqData.PoolID
	if !c.poolManager.Exists(poolName) {
		httprender.BadRequest(w, "pool not defined", logr)
		return
	}

	instance, err := c.poolManager.Provision(ctx, poolName)
	if err != nil {
		httprender.InternalError(w, "failed provisioning", err, logr)
		return
	}

	logr = logr.
		WithField("ip", instance.IP).
		WithField("id", instance.ID)

	// cleanUpFn is a function to terminate the instance if an error occurs later in the handleSetup function
	cleanUpFn := func() {
		errCleanUp := c.poolManager.Destroy(context.Background(), poolName, instance.ID)
		if errCleanUp != nil {
			logr.WithError(errCleanUp).Errorln("failed to delete failed instance client")
		}
	}

	tags := map[string]string{}
	for k, v := range reqData.Tags {
		if strings.HasPrefix(k, vmpool.TagPrefix) {
			continue
		}
		tags[k] = v
	}
	tags[TagStageID] = reqData.ID

	err = c.poolManager.Tag(ctx, poolName, instance.ID, tags)
	if err != nil {
		httprender.InternalError(w, "failed to tag", err, logr)
		go cleanUpFn()
		return
	}

	client, err := c.getLEClient(instance.IP)
	if err != nil {
		httprender.InternalError(w, "failed to create LE client", err, logr)
		go cleanUpFn()
		return
	}

	const timeoutSetup = 20 * time.Minute // TODO: Move to configuration

	// try the healthcheck api on the lite-engine until it responds ok
	logr.Traceln("running healthcheck and waiting for an ok response")
	if _, err = client.RetryHealth(ctx, timeoutSetup); err != nil {
		httprender.InternalError(w, "failed to call lite-engine retry health", err, logr)
		go cleanUpFn()
		return
	}

	logr.Traceln("retry health check complete")

	setupResponse, err := client.Setup(ctx, &reqData.SetupRequest)
	if err != nil {
		httprender.InternalError(w, "failed to call setup lite-engine", err, logr)
		go cleanUpFn()
		return
	}

	logr.WithField("response", fmt.Sprintf("%+v", setupResponse)).
		Traceln("VM setup is complete")

	httprender.OK(w, struct {
		InstanceID string `json:"instance_id,omitempty"`
		IPAddress  string `json:"ip_address,omitempty"`
	}{
		IPAddress:  instance.IP,
		InstanceID: instance.ID,
	})
}

func (c *delegateCommand) handleStep(w http.ResponseWriter, r *http.Request) {
	reqData := &struct {
		ID                     string `json:"id"`
		IPAddress              string `json:"ip_address"`
		PoolID                 string `json:"pool_id"`
		CorrelationID          string `json:"correlation_id"`
		leapi.StartStepRequest `json:"start_step_request"`
	}{}

	if err := getJSONDataFromReader(r.Body, reqData); err != nil {
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	if reqData.ID == "" && reqData.IPAddress == "" {
		httprender.BadRequest(w, "either parameter 'id' or 'ip_address' must be provided", nil)
		return
	}

	if reqData.PoolID == "" {
		httprender.BadRequest(w, "mandatory field 'pool_id' in the request body is empty", nil)
		return
	}

	logr := logrus.
		WithField("api", "delegate:step").
		WithField("step_id", reqData.StartStepRequest.ID).
		WithField("pool", reqData.PoolID).
		WithField("correlation_id", reqData.CorrelationID)

	ctx := r.Context()

	var ipAddress string

	if reqData.IPAddress != "" {
		ipAddress = reqData.IPAddress
	} else {
		inst, err := c.poolManager.GetUsedInstanceByTag(ctx, reqData.PoolID, TagStageID, reqData.ID)
		if err != nil {
			httprender.InternalError(w, "cannot get the instance by tag", err, logr)
			return
		}
		if inst == nil || inst.IP == "" {
			httprender.NotFound(w, "instance with provided ID not found", logr)
			return
		}

		ipAddress = inst.IP
	}

	logr = logr.
		WithField("ip", ipAddress)

	client, err := c.getLEClient(ipAddress)
	if err != nil {
		httprender.InternalError(w, "failed to create client", err, logr)
		return
	}

	logr.Traceln("running StartStep")

	startStepResponse, err := client.StartStep(ctx, &reqData.StartStepRequest)
	if err != nil {
		httprender.InternalError(w, "failed to call LE.StartStep", err, logr)
		return
	}

	logr.WithField("startStepResponse", startStepResponse).
		Traceln("LE.StartStep complete")

	const timeoutStep = 4 * time.Hour // TODO: Move to configuration

	pollResponse, err := client.RetryPollStep(ctx, &leapi.PollStepRequest{ID: reqData.StartStepRequest.ID}, timeoutStep)
	if err != nil {
		httprender.InternalError(w, "failed to call LE.RetryPollStep", err, logr)
		return
	}

	logr.WithField("pollResponse", pollResponse).
		Traceln("LE.RetryPollStep complete")

	httprender.OK(w, pollResponse)
}

func (c *delegateCommand) handleDestroy(w http.ResponseWriter, r *http.Request) {
	reqData := &struct {
		ID            string `json:"id"`
		InstanceID    string `json:"instance_id"`
		PoolID        string `json:"pool_id"`
		CorrelationID string `json:"correlation_id"`
	}{}

	if err := getJSONDataFromReader(r.Body, reqData); err != nil {
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	if reqData.ID == "" && reqData.InstanceID == "" {
		httprender.BadRequest(w, "either parameter 'id' or 'instance_id' must be provided", nil)
		return
	}

	if reqData.PoolID == "" {
		httprender.BadRequest(w, "mandatory field 'pool_id' in the request body is empty", nil)
		return
	}

	ctx := r.Context()

	logr := logrus.
		WithField("api", "delegate:destroy").
		WithField("id", reqData.ID).
		WithField("pool", reqData.PoolID).
		WithField("correlation_id", reqData.CorrelationID)

	var instanceID string

	if reqData.InstanceID != "" {
		instanceID = reqData.InstanceID
	} else {
		inst, err := c.poolManager.GetUsedInstanceByTag(ctx, reqData.PoolID, TagStageID, reqData.ID)
		if err != nil {
			httprender.InternalError(w, "cannot get the instance by tag", err, logr)
			return
		}
		if inst == nil {
			httprender.NotFound(w, "instance with provided ID not found", logr)
			return
		}

		instanceID = inst.ID
	}

	logr = logr.
		WithField("instance_id", instanceID)

	if err := c.poolManager.Destroy(ctx, reqData.PoolID, instanceID); err != nil {
		httprender.InternalError(w, "cannot destroy the instance", err, logr)
		return
	}

	logr.Traceln("destroyed instance")

	w.WriteHeader(http.StatusOK)
}

func RegisterDelegate(app *kingpin.Application) {
	c := new(delegateCommand)

	c.poolManager = &vmpool.Manager{}

	cmd := app.Command("delegate", "starts the delegate").
		Action(c.run)
	cmd.Flag("envfile", "load the environment variable file").
		Default("").
		StringVar(&c.envfile)
	cmd.Flag("poolfile", "file to seed the aws pool").
		Default(".drone_pool.yml").
		StringVar(&c.awsPoolfile)
	cmd.Flag("pool_file_google", "file to seed the google pool").
		Default(".drone_pool_google.yml").
		StringVar(&c.googlePoolFile)
}

func (c *delegateCommand) getLEClient(instanceIP string) (*lehttp.HTTPClient, error) {
	leURL := fmt.Sprintf("https://%s:9079/", instanceIP)

	return lehttp.NewHTTPClient(leURL,
		c.defaultPoolSettings.RunnerName, c.defaultPoolSettings.CaCertFile,
		c.defaultPoolSettings.CertFile, c.defaultPoolSettings.KeyFile)
}

func getJSONDataFromReader(r io.Reader, data interface{}) error {
	if err := json.NewDecoder(r).Decode(data); err != nil {
		err = fmt.Errorf("failed to parse request JSON body: %w", err)
		return err
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
