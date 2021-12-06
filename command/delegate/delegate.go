// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package delegate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/daemon"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/le"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"

	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/server"
	"github.com/drone/signal"
	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
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

const TagCorrelationID = "correlation-id"

func (c *delegateCommand) run(*kingpin.ParseContext) error {
	// load environment variables from file.
	envError := godotenv.Load(c.envfile)
	if envError != nil {
		logrus.WithError(envError).
			Errorln("failed to load environment variables")
	}
	// load the configuration from the environment
	var config daemon.Config
	processEnvErr := envconfig.Process("", &config)
	if processEnvErr != nil {
		logrus.WithError(processEnvErr).
			Errorln("failed to load configuration")
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
			Errorln("failed to generate certificates")
		return certGenerationErr
	}
	// read cert files into memory
	var readCertsErr error
	config.DefaultPoolSettings.CaCertFile, config.DefaultPoolSettings.CertFile, config.DefaultPoolSettings.KeyFile, readCertsErr = le.ReadLECerts(config.DefaultPoolSettings.CertificateFolder)
	if readCertsErr != nil {
		logrus.WithError(readCertsErr).
			Errorln("failed to read certificates")
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
		WithField("capacity", config.Runner.Capacity).
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

	mux.Post("/setup", c.handleSetup())
	mux.Post("/destroy", c.handleDestroy())
	mux.Post("/step", c.handleStep())
	mux.Post("/pool_owner", c.handlePoolOwner())

	return mux
}

func (c *delegateCommand) handlePoolOwner() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keys, ok := r.URL.Query()["pool"]
		if !ok || len(keys[0]) < 1 {
			fmt.Println("Url Param 'pool' is missing")
			http.Error(w, "Url Param 'pool' is missing", http.StatusBadRequest)
			return
		}

		poolName := keys[0] // Query()["key"] will return an array of items, we only want the single item.

		JSON(w, struct {
			Owner bool `json:"owner"`
		}{
			Owner: c.poolManager.Exists(poolName),
		}, http.StatusOK)
	}
}

func (c *delegateCommand) handleSetup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqData, err := GetSetupRequest(r.Body)
		if err != nil {
			logrus.Debugln("handleSetup: failed to read setup request")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		poolName := reqData.PoolID
		if !c.poolManager.Exists(poolName) {
			logrus.Debugln("handleSetup: failed to find pool")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		ctx := r.Context()

		instance, err := c.poolManager.Provision(ctx, poolName)
		if err != nil {
			logrus.WithError(err).
				WithField("pool", poolName).
				Errorln("handleSetup: failed provisioning")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		logr := logrus.
			WithField("pool", poolName).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("correlation_id", reqData.CorrelationID)

		cleanUpFn := func() {
			errCleanUp := c.poolManager.Destroy(context.Background(), poolName, instance.ID)
			if errCleanUp != nil {
				logr.WithError(errCleanUp).Errorln("handleSetup: failed to delete failed instance client")
			}
		}

		tags := map[string]string{}
		for k, v := range reqData.Tags {
			if strings.HasPrefix(k, vmpool.TagPrefix) {
				continue
			}
			tags[k] = v
		}
		tags[TagCorrelationID] = reqData.CorrelationID

		err = c.poolManager.Tag(ctx, poolName, instance.ID, tags)
		if err != nil {
			logr.WithError(err).Errorln("handleSetup: failed to tag")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			go cleanUpFn()
			return
		}

		client, err := c.getLEClient(instance.IP)
		if err != nil {
			logr.WithError(err).Errorln("handleStep: failed to create client")
			w.WriteHeader(http.StatusInternalServerError)
			go cleanUpFn()
			return
		}

		// try the healthcheck api on the lite-engine until it responds ok
		logr.Debugln("handleSetup: running healthcheck and waiting for an ok response")
		healthResponse, err := client.RetryHealth(ctx)
		if err != nil {
			logr.WithError(err).Errorln("handleSetup: RetryHealth call failed")
			w.WriteHeader(http.StatusInternalServerError)
			go cleanUpFn()
			return
		}

		logr.WithField("response", *healthResponse).Infoln("handleSetup: health check complete")
		setupResponse, err := client.Setup(ctx, &reqData.SetupRequest)
		if err != nil {
			logr.WithError(err).Errorln("handleSetup: setup call failed")
			w.WriteHeader(http.StatusInternalServerError)
			go cleanUpFn()
			return
		}

		logr.WithField("response", *setupResponse).Infoln("handleSetup: setup complete")

		JSON(w, struct {
			InstanceID string `json:"instance_id,omitempty"`
			IPAddress  string `json:"ip_address,omitempty"`
		}{
			IPAddress:  instance.IP,
			InstanceID: instance.ID,
		}, http.StatusOK)
	}
}

func (c *delegateCommand) handleStep() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqData, err := GetExecStepRequest(r.Body)
		if err != nil {
			logrus.Error("handleStep: failed to read step request")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		ctx := r.Context()

		var ipAddress string

		if reqData.IPAddress != "" {
			ipAddress = reqData.IPAddress
		} else if reqData.CorrelationID != "" && reqData.PoolID != "" {
			var inst *vmpool.Instance
			inst, err = c.poolManager.GetUsedInstanceByTag(ctx, reqData.PoolID, TagCorrelationID, reqData.CorrelationID)
			if err != nil {
				logrus.WithError(err).Errorln("handleStep: cannot get the instance by tag")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if inst == nil {
				logrus.Debugln("handleStep: instance with provided correlation ID not found")
				http.Error(w, "instance not found", http.StatusNotFound)
				return
			}

			ipAddress = inst.IP
		} else {
			logrus.Debugln("handleStep: missing instance IP or correlation ID and pool ID")
			http.Error(w, "missing instance IP or correlation ID and pool ID", http.StatusBadRequest)
			return
		}

		logr := logrus.
			WithField("ip", ipAddress).
			WithField("step_id", reqData.StartStepRequest.ID).
			WithField("pool", reqData.PoolID).
			WithField("correlation_id", reqData.CorrelationID)

		client, err := c.getLEClient(ipAddress)
		if err != nil {
			logr.WithError(err).Errorln("handleStep: failed to create client")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		logr.Debugln("handleStep: running StartStep")

		startStepResponse, stepErr := client.StartStep(ctx, &reqData.StartStepRequest)
		if stepErr != nil {
			logrus.WithError(stepErr).Errorln("handleStep: StartStep call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		logr.WithField("startStepResponse", startStepResponse).
			Debugln("handleStep: StartStep complete")

		pollResponse, stepErr := client.RetryPollStep(ctx, &api.PollStepRequest{ID: reqData.StartStepRequest.ID})
		if stepErr != nil {
			logr.WithError(stepErr).Errorln("handleStep: RetryPollStep call failed")
			w.WriteHeader(http.StatusInternalServerError)
		}

		logr.WithField("pollResponse", pollResponse).Debugln("handleStep: RetryPollStep complete")

		JSON(w, pollResponse, http.StatusOK)
	}
}

func (c *delegateCommand) handleDestroy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqData, err := GetDestroyRequest(r.Body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		ctx := r.Context()

		logr := logrus.
			WithField("id", reqData.InstanceID).
			WithField("correlation_id", reqData.CorrelationID)

		var instanceID string

		if reqData.InstanceID != "" {
			instanceID = reqData.InstanceID
		} else if reqData.CorrelationID != "" && reqData.PoolID != "" {
			var inst *vmpool.Instance
			inst, err = c.poolManager.GetUsedInstanceByTag(ctx, reqData.PoolID, TagCorrelationID, reqData.CorrelationID)
			if err != nil {
				logr.WithError(err).Errorln("handleDestroy: cannot get the instance by tag")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if inst == nil {
				logrus.Debugln("handleDestroy: instance with provided correlation ID not found")
				http.Error(w, "instance not found", http.StatusNotFound)
				return
			}

			instanceID = inst.ID
		} else {
			logr.Debugln("handleDestroy: missing instance ID or correlation ID")
			http.Error(w, "missing instance ID or correlation ID", http.StatusBadRequest)
			return
		}

		err = c.poolManager.Destroy(ctx, reqData.PoolID, instanceID)
		if err != nil {
			logr.WithError(err).Errorln("cannot destroy the instance")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		logr.Debugln("handleDestroy: destroyed instance")

		w.WriteHeader(http.StatusOK)
	}
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

func JSON(w http.ResponseWriter, v interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	err := enc.Encode(v)
	if err != nil {
		return
	}
}

func (c *delegateCommand) getLEClient(instanceIP string) (*lehttp.HTTPClient, error) {
	leURL := fmt.Sprintf("https://%s:9079/", instanceIP)

	return lehttp.NewHTTPClient(leURL,
		c.defaultPoolSettings.RunnerName, c.defaultPoolSettings.CaCertFile,
		c.defaultPoolSettings.CertFile, c.defaultPoolSettings.KeyFile)
}
