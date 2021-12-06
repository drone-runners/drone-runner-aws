// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package delegate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

type (
	delegateCommand struct {
		envfile             string
		awsPoolfile         string
		defaultPoolSettings vmpool.DefaultSettings
		poolManager         *vmpool.Manager
	}
)

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
	const busyMaxAge = time.Hour
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
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", c.handleSetup())
	mux.HandleFunc("/destroy", c.handleDestroy())
	mux.HandleFunc("/step", c.handleStep())
	mux.HandleFunc("/pool_owner", c.handlePoolOwner())
	return mux
}

func (c *delegateCommand) handlePoolOwner() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			fmt.Println("failed to read setup get request")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		keys, ok := r.URL.Query()["pool"]

		if !ok || len(keys[0]) < 1 {
			fmt.Println("Url Param 'pool' is missing")
			http.Error(w, "Url Param 'pool' is missing", http.StatusBadRequest)
			return
		}

		// Query()["key"] will return an array of items, we only want the single item.
		poolName := keys[0]
		fmt.Println("pool: ", poolName)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		type Response struct {
			Owner bool `json:"owner"`
		}

		response := Response{
			Owner: c.poolManager.Exists(poolName),
		}

		_ = json.NewEncoder(w).Encode(response)
	}
}

func (c *delegateCommand) handleSetup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// check our input
		if r.Method != http.MethodPost {
			logrus.Error("handleSetup: failed to read setup post request")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		reqData, err := GetSetupRequest(r.Body)
		if err != nil {
			logrus.Error("handleSetup: failed to read setup request")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		poolName := reqData.PoolID
		if !c.poolManager.Exists(poolName) {
			logrus.Error("handleSetup: failed to find pool")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		instance, err := c.poolManager.Provision(r.Context(), poolName)
		if err != nil {
			logrus.WithError(err).
				WithField("pool", poolName).
				Errorf("handleSetup: failed provisioning")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		// create client to lite-engine
		client, err := lehttp.NewHTTPClient(
			fmt.Sprintf("https://%s:9079/", instance.IP),
			c.defaultPoolSettings.RunnerName, c.defaultPoolSettings.CaCertFile, c.defaultPoolSettings.CertFile, c.defaultPoolSettings.KeyFile)
		if err != nil {
			logrus.WithError(err).
				Errorln("handleSetup: failed to create client")
			return
		}
		// try the healthcheck api on the lite-engine until it responds ok
		logrus.
			WithField("pool", poolName).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("handleSetup: running healthcheck and waiting for an ok response")
		healthResponse, healthErr := client.RetryHealth(r.Context())
		if healthErr != nil {
			logrus.
				WithField("pool", poolName).
				WithField("ip", instance.IP).
				WithField("id", instance.ID).
				WithError(healthErr).
				Errorln("handleSetup: RetryHealth call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logrus.
			WithField("pool", poolName).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("response", *healthResponse).
			Info("handleSetup: health check complete")
		liteEngineSetupResponse, setupErr := client.Setup(r.Context(), &reqData.SetupRequest)
		if setupErr != nil {
			logrus.WithError(setupErr).
				WithField("pool", poolName).
				WithField("ip", instance.IP).
				WithField("id", instance.ID).
				Errorln("handleSetup: setup call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		JSON(w, struct {
			InstanceID string `json:"instance_id,omitempty"`
			IPAddress  string `json:"ip_address,omitempty"`
		}{
			IPAddress:  instance.IP,
			InstanceID: instance.ID,
		}, http.StatusOK)
		logrus.
			WithField("pool", poolName).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("response", *liteEngineSetupResponse).
			Info("handleSetup: setup complete")
	}
}

func (c *delegateCommand) handleStep() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// check input
		if r.Method != http.MethodPost {
			logrus.Error("handleStep: failed to read setup step request")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		reqData, err := GetExecStepRequest(r.Body)
		if err != nil {
			logrus.Error("handleStep: failed to read step request")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		instanceIP := reqData.IPAddress
		if instanceIP == "" {
			logrus.Error("handleStep: failed to read instance ip")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		client, err := lehttp.NewHTTPClient(
			fmt.Sprintf("https://%s:9079/", instanceIP),
			c.defaultPoolSettings.RunnerName, c.defaultPoolSettings.CaCertFile, c.defaultPoolSettings.CertFile, c.defaultPoolSettings.KeyFile)
		if err != nil {
			logrus.WithError(err).
				Errorln("handleStep: failed to create client")
			return
		}
		logrus.
			WithField("ip", instanceIP).
			WithField("step_id", reqData.StartStepRequest.ID).
			Debug("handleStep: running StartStep")
		startStepResponse, stepErr := client.StartStep(r.Context(), &reqData.StartStepRequest)
		if stepErr != nil {
			logrus.WithError(stepErr).
				WithField("ip", instanceIP).
				WithField("step_id", reqData.StartStepRequest.ID).
				Errorln("handleStep: StartStep call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logrus.
			WithField("startStepResponse", startStepResponse).
			Debug("handleStep: StartStep complete")

		pollResponse, stepErr := client.RetryPollStep(r.Context(), &api.PollStepRequest{ID: reqData.StartStepRequest.ID})
		if stepErr != nil {
			logrus.WithError(stepErr).
				WithField("ip", instanceIP).
				WithField("step_id", reqData.StartStepRequest.ID).
				Errorln("handleStep: RetryPollStep call failed")
			w.WriteHeader(http.StatusInternalServerError)
		}
		logrus.
			WithField("pollResponse", pollResponse).
			Debug("handleStep: RetryPollStep complete")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		encodeError := json.NewEncoder(w).Encode(pollResponse)
		if encodeError != nil {
			logrus.WithError(encodeError).
				WithField("ip", instanceIP).
				WithField("step_id", reqData.StartStepRequest.ID).
				Errorln("handleStep: failed to encode poll response")
		}
	}
}

func (c *delegateCommand) handleDestroy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		reqData, err := GetDestroyRequest(r.Body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		fmt.Printf("\n\nExecuting cleanup: %v\n", reqData)
		err = c.poolManager.Destroy(r.Context(), reqData.PoolID, reqData.InstanceID)
		if err != nil {
			logrus.WithError(err).
				Errorln("cannot destroy the instance")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

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
