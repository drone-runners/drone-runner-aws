// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package delegate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

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
	"github.com/harness/lite-engine/engine/spec"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gopkg.in/alecthomas/kingpin.v2"
)

type delegateCommand struct {
	envfile     string
	poolfile    string
	runnerName  string
	awsSettings cloudaws.AccessSettings
}

func (c *delegateCommand) run(*kingpin.ParseContext) error { // nolint: funlen, gocyclo
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
	// set runner name
	c.runnerName = config.Runner.Name
	// setup the global logrus logger.
	daemon.SetupLogger(&config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// listen for termination signals to gracefully shutdown the runner.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	if (config.Settings.PrivateKeyFile != "" && config.Settings.PublicKeyFile == "") || (config.Settings.PrivateKeyFile == "" && config.Settings.PublicKeyFile != "") {
		logrus.Fatalln("delegate: specify a private key file and public key file or leave both settings empty to generate keys")
	}

	certGenerationErr := le.GenerateLECerts(config.Runner.Name, config.Settings.CertificateFolder)
	if certGenerationErr != nil {
		logrus.WithError(processEnvErr).
			Errorln("failed to generate certificates")
	}

	ce, err := le.ReadLECerts(config.Settings.CertificateFolder)
	if err != nil {
		return nil
	}

	awsAccessSettings := cloudaws.AccessSettings{
		AccessKey:      config.Settings.AwsAccessKeyID,
		AccessSecret:   config.Settings.AwsAccessKeySecret,
		Region:         config.Settings.AwsRegion,
		PrivateKeyFile: config.Settings.PrivateKeyFile,
		PublicKeyFile:  config.Settings.PublicKeyFile,
		LiteEnginePath: config.Settings.LiteEnginePath,
		CaCertFile:     ce.CaCertFile,
		CertFile:       ce.CertFile,
		KeyFile:        ce.KeyFile,
	}

	c.awsSettings = awsAccessSettings

	// read cert files into memory
	pools, poolFileErr := cloudaws.ProcessPoolFile(c.poolfile, &awsAccessSettings, config.Runner.Name)
	if poolFileErr != nil {
		logrus.WithError(poolFileErr).
			Errorln("delegate: unable to parse pool file")
		os.Exit(1) //nolint:gocritic // failing fast before we do any work.
	}

	poolManager := &vmpool.Manager{}
	err = poolManager.Add(pools...)
	if err != nil {
		return err
	}

	err = poolManager.Ping(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: cannot connect to cloud provider")
		return err
	}
	// lets remove any old instances.
	if !config.Settings.ReusePool {
		cleanErr := poolManager.CleanPools(ctx)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("delegate: unable to clean pools")
		} else {
			logrus.Infoln("delegate: pools cleaned")
		}
	}
	// seed a pool
	err = poolManager.BuildPools(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: unable to build pool")
		os.Exit(1)
	}
	logrus.Infoln("delegate: pool created")

	hook := loghistory.New()
	logrus.AddHook(hook)

	var g errgroup.Group
	runnerServer := server.Server{
		Addr:    config.Server.Port,
		Handler: c.delegateListener(poolManager),
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
	return waitErr
}

func (c *delegateCommand) delegateListener(poolManager *vmpool.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", c.handleSetup(poolManager))
	mux.HandleFunc("/destroy", c.handleDestroy(poolManager))
	mux.HandleFunc("/step", c.handleStep())
	mux.HandleFunc("/pool_owner", handlePoolOwner(poolManager))
	return mux
}

func handlePoolOwner(poolManager *vmpool.Manager) http.HandlerFunc {
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
		pool := keys[0]
		fmt.Println("pool: ", pool)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		type Response struct {
			Owner bool `json:"owner"`
		}

		response := Response{
			Owner: poolManager.Get(pool) != nil,
		}
		_ = json.NewEncoder(w).Encode(response)
	}
}

func (c *delegateCommand) handleSetup(poolManager *vmpool.Manager) http.HandlerFunc { //nolint:funlen
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
		pool := poolManager.Get(reqData.Pool)
		if pool == nil {
			logrus.Error("handleSetup: failed to find pool")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		// get an instance
		logrus.
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", pool.GetName()).
			Debug("handleSetup: starting setup")
		instance, tryPoolErr := pool.TryPool(r.Context())
		if tryPoolErr != nil {
			logrus.WithError(tryPoolErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", pool.GetName()).
				Errorf("handleSetup: failed trying pool")
		}
		if instance != nil {
			// using the pool, use the provided keys
			logrus.
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", pool.GetName()).
				WithField("ip", instance.IP).
				WithField("id", instance.ID).
				Debug("handleSetup: got a pool instance")
		} else {
			logrus.
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", pool.GetName()).
				Debug("handleSetup: pool empty, creating an adhoc instance")

			var provisionErr error
			instance, provisionErr = pool.Provision(r.Context(), true)
			if provisionErr != nil {
				logrus.WithError(provisionErr).
					WithField("ami", pool.GetInstanceType()).
					WithField("pool", pool.GetName()).
					Errorf("handleSetup: failed provisioning")
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
		// create client to lite-engine
		client, err := lehttp.NewHTTPClient(
			fmt.Sprintf("https://%s:9079/", instance.IP),
			c.runnerName, c.awsSettings.CaCertFile, c.awsSettings.CertFile, c.awsSettings.KeyFile)
		if err != nil {
			logrus.WithError(err).
				Errorln("handleSetup: failed to create client")
			return
		}
		// try the healthcheck api on the lite-engine until it responds ok
		logrus.
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", pool.GetName()).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			Debug("handleSetup: running healthcheck and waiting for an ok response")
		healthResponse, healthErr := client.RetryHealth(r.Context())
		if healthErr != nil {
			logrus.
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", pool.GetName()).
				WithField("ip", instance.IP).
				WithField("id", instance.ID).
				WithError(healthErr).
				Errorln("handleSetup: RetryHealth call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logrus.
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", pool.GetName()).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("response", *healthResponse).
			Info("handleSetup: health check complete")
		// now setup the instance
		setupRequest := &api.SetupRequest{
			Platform: spec.Platform{
				OS: pool.GetOS(),
			},
			Network: spec.Network{
				ID: "drone",
			},
		}
		setupResponse, setupErr := client.Setup(r.Context(), setupRequest)
		if setupErr != nil {
			logrus.WithError(setupErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", pool.GetName()).
				WithField("ip", instance.IP).
				WithField("id", instance.ID).
				Errorln("handleSetup: setup call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logrus.
			WithField("ami", pool.GetInstanceType()).
			WithField("pool", pool.GetName()).
			WithField("ip", instance.IP).
			WithField("id", instance.ID).
			WithField("response", *setupResponse).
			Info("handleSetup: setup complete")
		w.WriteHeader(http.StatusOK)
		// we have successfully setup the environment lets replace the lost pool member
		// poolCount, countPoolErr := pool.PoolCountFree(r.Context())
		// if countPoolErr != nil {
		// 	logrus.WithError(countPoolErr).
		// 		Errorln("handleSetup: failed checking pool")
		// }
		// if poolCount < pool.GetMaxSize() {
		// 	instance, provisionErr := pool.Provision(r.Context(), false)
		// 	if provisionErr != nil {
		// 		logrus.WithError(provisionErr).
		// 			Errorln("handleSetup: failed to add back to the pool")
		// 	} else {
		// 		logrus.Debugf("handleSetup: add back to the pool %s %s", instance.ID, instance.IP)
		// 	}
		// }
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
			c.runnerName, c.awsSettings.CaCertFile, c.awsSettings.CertFile, c.awsSettings.KeyFile)
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

func (c *delegateCommand) handleDestroy(poolManager *vmpool.Manager) http.HandlerFunc {
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

		pool := poolManager.Get(reqData.Pool)
		instance := &vmpool.Instance{
			ID: reqData.ID,
			IP: "", // TODO remove this
		}
		destroyErr := pool.Destroy(r.Context(), instance)
		if destroyErr != nil {
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

	cmd := app.Command("delegate", "starts the delegate").
		Action(c.run)

	cmd.Arg("envfile", "load the environment variable file").
		Default("").
		StringVar(&c.envfile)
	cmd.Arg("poolfile", "file to seed the aws pool").
		Default(".drone_pool.yml").
		StringVar(&c.poolfile)
}
