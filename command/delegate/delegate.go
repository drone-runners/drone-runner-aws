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
	"github.com/drone-runners/drone-runner-aws/engine"
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
	envfile  string
	poolfile string
}

var delegateEngineOpts engine.Opts

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

	awsAccessSettings := cloudaws.AccessSettings{
		AccessKey:         config.Settings.AwsAccessKeyID,
		AccessSecret:      config.Settings.AwsAccessKeySecret,
		Region:            config.Settings.AwsRegion,
		PrivateKeyFile:    config.Settings.PrivateKeyFile,
		PublicKeyFile:     config.Settings.PublicKeyFile,
		LiteEnginePath:    config.Settings.LiteEnginePath,
		CertificateFolder: config.Settings.CertificateFolder,
	}

	certGenerationErr := le.GenerateLECerts(config.Runner.Name, config.Settings.CertificateFolder)
	if certGenerationErr != nil {
		logrus.WithError(processEnvErr).
			Errorln("failed to generate certificates")
	}

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

	delegateEngineOpts = engine.Opts{
		PoolManager: poolManager,
	}

	engineInstance, engineErr := engine.New(delegateEngineOpts)
	if engineErr != nil {
		logrus.WithError(engineErr).
			Errorln("cannot create engine")
		return engineErr
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
		Addr:    ":3000", // config.Server.Port,
		Handler: delegateListener(engineInstance, poolManager, config.Runner.Name, config.Settings.CertificateFolder),
	}

	logrus.WithField("addr", ":3000" /*config.Server.Port*/).
		Infoln("starting the server")

	g.Go(func() error {
		return runnerServer.ListenAndServe(ctx)
	})

	g.Go(func() error {
		logrus.WithField("capacity", config.Runner.Capacity).
			WithField("kind", resource.Kind).
			WithField("type", resource.Type).
			WithField("os", "linux" /*config.Platform.OS*/).
			WithField("arch", "amd64" /*config.Platform.Arch*/).
			Infoln("polling the remote server")
		return nil
	})

	waitErr := g.Wait()
	if waitErr != nil {
		logrus.WithError(waitErr).
			Errorln("shutting down the server")
	}
	return waitErr
}

func delegateListener(eng *engine.Engine, poolManager *vmpool.Manager, runnerName, certFolder string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", handleSetup(eng, poolManager))
	mux.HandleFunc("/destroy", handleDestroy(poolManager))
	mux.HandleFunc("/step", handleStep(runnerName, certFolder))
	mux.HandleFunc("/pool_owner", handlePools(poolManager))
	return mux
}

func handlePools(poolManager *vmpool.Manager) http.HandlerFunc {
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

func handleSetup(eng *engine.Engine, poolManager *vmpool.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			fmt.Println("handleSetup: failed to read setup post request")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		reqData, err := GetSetupRequest(r.Body)
		if err != nil {
			fmt.Println("handleSetup: failed to read setup request")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		pool := poolManager.Get(reqData.Pool)
		if pool == nil {
			fmt.Println("handleSetup: failed to find pool")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		fmt.Printf("handleSetup: Executing setup: %v\n", reqData)
		setupSpec, err := CompileDelegateSetupStage(pool)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = eng.Setup(r.Context(), setupSpec)
		if err != nil {
			logrus.WithError(err).
				Errorln("handleSetup: cannot setup the docker environment")
			w.WriteHeader(http.StatusInternalServerError)
		}

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

func handleStep(runnerName, certFolder string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			fmt.Println("failed to read setup step request")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		reqData, err := GetExecStepRequest(r.Body)
		if err != nil {
			fmt.Println("failed to read step request")
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		fmt.Printf("\n\nExecuting step: %v\n", reqData)
		stepID := reqData.StepID
		instanceIP := reqData.IP

		stepInstance := &api.StartStepRequest{
			ID:    stepID,
			Kind:  api.Run,
			Image: reqData.Image,
			Volumes: []*spec.VolumeMount{
				{
					Name: "_workspace",
					Path: "/tmp/aws",
				},
			},
			WorkingDir: "/tmp/aws",
		}

		stepInstance.Run.Command = []string{fmt.Sprintf("set -xe; pwd; %s", reqData.Command)}
		stepInstance.Run.Entrypoint = []string{"sh", "-c"}

		fmt.Fprintf(os.Stdout, "--- step=%s end --- vvv ---\n", stepID)
		client, err := lehttp.NewHTTPClient(
			fmt.Sprintf("https://%s:9079/", instanceIP),
			runnerName, fmt.Sprintf("%s/ca-cert.pem", certFolder), fmt.Sprintf("%s/server-cert.pem", certFolder), fmt.Sprintf("%s/server-key.pem", certFolder))
		if err != nil {
			logrus.WithError(err).
				Errorln("failed to create client")
			return
		}

		stepResponse, stepErr := client.StartStep(r.Context(), stepInstance)
		if stepErr != nil {
			logrus.WithError(stepErr).Errorln("start step1 call failed")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logrus.Infof("step response: %v\nPolling step", stepResponse)
		pollResponse, stepErr := client.PollStep(r.Context(), &api.PollStepRequest{ID: stepID})
		if stepErr != nil {
			logrus.WithError(stepErr).Errorln("poll step1 call failed")
			w.WriteHeader(http.StatusInternalServerError)
		}

		fmt.Fprintf(os.Stdout, "--- step=%s end --- ^^^ ---\n", stepID)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			_ = json.NewEncoder(w).Encode(pollResponse)
		}
	}
}

func handleDestroy(poolManager *vmpool.Manager) http.HandlerFunc {
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
