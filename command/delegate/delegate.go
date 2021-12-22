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

	"github.com/drone-runners/drone-runner-aws/command/daemon"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/httprender"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"

	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/server"
	"github.com/drone/signal"
	"github.com/harness/lite-engine/api"
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
	defaultPoolSettings vmpool.DefaultSettings
	poolManager         *vmpool.Manager
}

const TagStageID = vmpool.TagPrefix + "stage-id"

func (c *delegateCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envfile)
	if envError != nil {
		logrus.WithError(envError).
			Errorln("delegate: failed to load environment variables")
	}
	// load the configuration from the environment
	var config daemon.Config // TODO: Do not use daemon config, use delegate config
	processEnvErr := envconfig.Process("", &config)
	if processEnvErr != nil {
		logrus.WithError(processEnvErr).
			Errorln("delegate: failed to load configuration")
	}
	// load the configuration from the environment
	config, err := daemon.FromEnviron()
	if err != nil {
		return err
	}
	// setup the global logrus logger.
	daemon.SetupLogger(&config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	if (config.DefaultPoolSettings.PrivateKeyFile != "" && config.DefaultPoolSettings.PublicKeyFile == "") ||
		(config.DefaultPoolSettings.PrivateKeyFile == "" && config.DefaultPoolSettings.PublicKeyFile != "") {
		logrus.Fatalln("delegate: specify a private key file and public key file or leave both settings empty to generate keys")
	}
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
		RunnerName:         config.Runner.Name,
		PrivateKeyFile:     config.DefaultPoolSettings.PrivateKeyFile,
		PublicKeyFile:      config.DefaultPoolSettings.PublicKeyFile,
		AwsAccessKeyID:     config.DefaultPoolSettings.AwsAccessKeyID,
		AwsAccessKeySecret: config.DefaultPoolSettings.AwsAccessKeySecret,
		AwsRegion:          config.DefaultPoolSettings.AwsRegion,
		AwsKeyPairName:     config.DefaultPoolSettings.AwsKeyPairName,
		LiteEnginePath:     config.DefaultPoolSettings.LiteEnginePath,
		CaCertFile:         config.DefaultPoolSettings.CaCertFile,
		CertFile:           config.DefaultPoolSettings.CertFile,
		KeyFile:            config.DefaultPoolSettings.KeyFile,
	}
	// process the pool file
	pools, poolFileErr := cloudaws.ProcessPoolFile(c.awsPoolfile, &c.defaultPoolSettings)
	if poolFileErr != nil {
		logrus.WithError(poolFileErr).
			Errorln("delegate: unable to parse pool file")
		return poolFileErr
	}

	err = c.poolManager.Add(pools...)
	if err != nil {
		return err
	}

	err = c.poolManager.Ping(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: cannot connect to cloud provider")
		return err
	}

	// TODO: Move the durations to config
	const busyMaxAge = time.Hour * 2 // includes time required to setup an instance
	const freeMaxAge = time.Hour * 12

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

			reqStart := time.Now()
			next.ServeHTTP(wrap, r)

			status := wrap.Status()
			dur := time.Since(reqStart).Milliseconds()

			logr := logrus.WithContext(r.Context()).
				WithField("time", time.Now().UTC().Format(time.RFC3339)).
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
		ID               string            `json:"id"`
		PoolID           string            `json:"pool_id"`
		Tags             map[string]string `json:"tags"`
		CorrelationID    string            `json:"correlation_id"`
		LogKey           string            `json:"log_key"`
		api.SetupRequest `json:"setup_request"`
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

		logr = log.WithContext(r.Context()).WithField("pool", reqData.PoolID)
	}

	poolName := reqData.PoolID
	if !c.poolManager.Exists(poolName) {
		httprender.BadRequest(w, "pool not defined", logr)
		return
	}

	ctx := r.Context()

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
	if _, err := client.RetryHealth(ctx, timeoutSetup); err != nil {
		httprender.InternalError(w, "failed to call LE.RetryHealth", err, logr)
		go cleanUpFn()
		return
	}

	logr.Traceln("LE.RetryHealth check complete")

	setupResponse, err := client.Setup(ctx, &reqData.SetupRequest)
	if err != nil {
		httprender.InternalError(w, "failed to call LE.Setup", err, logr)
		go cleanUpFn()
		return
	}

	logr.WithField("response", fmt.Sprintf("%+v", setupResponse)).
		Traceln("LE.Setup complete")

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
		ID                   string `json:"id"`
		IPAddress            string `json:"ip_address"`
		PoolID               string `json:"pool_id"`
		CorrelationID        string `json:"correlation_id"`
		api.StartStepRequest `json:"start_step_request"`
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

	pollResponse, err := client.RetryPollStep(ctx, &api.PollStepRequest{ID: reqData.StartStepRequest.ID}, timeoutStep)
	if err != nil {
		httprender.InternalError(w, "failed to call LE.RetryPollStep", err, logr)
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

func getStreamLogger(cfg api.LogConfig, logKey, correlationID string) *lelivelog.Writer {
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
