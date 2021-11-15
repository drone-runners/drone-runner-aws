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

	"github.com/drone-runners/drone-runner-aws/command/daemon"
	"github.com/drone-runners/drone-runner-aws/command/delegate/livelog"
	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool/cloudaws"

	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/logger"
	loghistory "github.com/drone/runner-go/logger/history"
	"github.com/drone/runner-go/server"

	"github.com/drone/signal"
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

	config.Runner.Name = "delegate"
	// setup the global logrus logger.
	daemon.SetupLogger(&config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// listen for termination signals to gracefully shutdown
	// the runner daemon.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	if (config.Settings.PrivateKeyFile != "" && config.Settings.PublicKeyFile == "") || (config.Settings.PrivateKeyFile == "" && config.Settings.PublicKeyFile != "") {
		logrus.Fatalln("delegate: specify a private key file and public key file or leave both settings empty to generate keys")
	}

	awsAccessSettings := cloudaws.AccessSettings{
		AccessKey:      config.Settings.AwsAccessKeyID,
		AccessSecret:   config.Settings.AwsAccessKeySecret,
		Region:         config.Settings.AwsRegion,
		PrivateKeyFile: config.Settings.PrivateKeyFile,
		PublicKeyFile:  config.Settings.PublicKeyFile,
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

	// if there is no keyfiles lets remove any old instances.
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
		Handler: delegateListener(engineInstance, poolManager),
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

func delegateListener(eng *engine.Engine, poolManager *vmpool.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/setup", handleSetup(eng, poolManager))
	mux.HandleFunc("/destroy", handleDestroy(eng))
	mux.HandleFunc("/step", handleStep(eng))
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
		stageID := reqData.StageID

		spec, err := CompileDelegateSetupStage(pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = Stages.Store(stageID, spec, reqData.StageEnvVars, reqData.SecretEnvVars)
		if err != nil {
			logrus.WithError(err).
				Errorln("handleSetup: failed to store spec")
			w.WriteHeader(http.StatusInternalServerError)
		}

		err = eng.Setup(r.Context(), spec)
		if err != nil {
			logrus.WithError(err).
				Errorln("handleSetup: cannot setup the docker environment")
			w.WriteHeader(http.StatusInternalServerError)
		}

		w.WriteHeader(http.StatusOK)
		// we have successfully setup the environment lets replace the lost pool member
		poolCount, countPoolErr := pool.PoolCountFree(r.Context())
		if countPoolErr != nil {
			logger.FromContext(r.Context()).
				WithError(countPoolErr).
				WithField("ami", pool.GetInstanceType()).
				WithField("pool", pool.GetName()).
				Errorf("handleSetup: failed checking pool")
		}
		if poolCount < pool.GetMaxSize() {
			instance, provisionErr := pool.Provision(r.Context(), false)
			if provisionErr != nil {
				logger.FromContext(r.Context()).
					WithError(provisionErr).
					WithField("ami", pool.GetInstanceType()).
					WithField("pool", pool.GetName()).
					Errorf("handleSetup: failed to add back to the pool")
			} else {
				logger.FromContext(r.Context()).
					WithField("ami", pool.GetInstanceType()).
					WithField("pool", pool.GetName()).
					WithField("ip", instance.IP).
					WithField("id", instance.ID).
					Debug("handleSetup: add back to the pool")
			}
		}
	}
}

func handleStep(eng *engine.Engine) http.HandlerFunc { // nolint: funlen
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
		stageID := reqData.StageID

		spec, envVars, secretVars, err := Stages.Get(stageID)
		if err != nil {
			logrus.WithError(err).
				Errorln("failed to get the stage")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if spec == nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		// create a step to run, why do we do this ? why not use the engine.spec
		steppy := engine.Step{
			//	ID:         stepID,
			Name:       reqData.StepID,
			WorkingDir: "/drone/src",
			Image:      reqData.Image,
			Envs:       environ.Combine(reqData.EnvVars, envVars, secretVars),
		}

		// this only handles a single command run on the ec2 instance
		if reqData.Command != "" {
			steppy.Command = reqData.Command
		}

		mount := &engine.VolumeMount{
			Name: "_workspace",
			Path: "/drone/src",
		}

		steppy.Volumes = append(steppy.Volumes, mount)

		for name, value := range secretVars {
			steppy.Secrets = append(steppy.Secrets, &engine.Secret{
				Name: name,
				Env:  name,
				Data: []byte(value),
				Mask: true,
			})
		}

		logStreamURL := reqData.LogStreamURL
		if logStreamURL == "" {
			logStreamURL = "http://localhost:8079"
		}

		logStreamAccountID := reqData.LogStreamAccountID
		if logStreamAccountID == "" {
			logStreamAccountID = "accountID"
		}

		logStreamToken := reqData.LogStreamToken
		if logStreamToken == "" {
			logStreamToken = "token"
		}

		c := livelog.NewHTTPClient(logStreamURL, logStreamAccountID, logStreamToken, true)

		// create a writer
		wc := livelog.New(c, reqData.LogKey)
		defer wc.Close()

		out := io.MultiWriter(wc, os.Stdout)
		fmt.Fprintf(os.Stdout, "--- step=%s end --- vvv ---\n", steppy.Name)

		state, err := eng.Run(r.Context(), spec, &steppy, out)
		if err != nil {
			logrus.WithError(err).
				Errorln("running the step failed. this is a runner error")
		}

		fmt.Fprintf(os.Stdout, "--- step=%s end --- ^^^ ---\n", steppy.Name)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			_ = json.NewEncoder(w).Encode(state)
		}
	}
}

func handleDestroy(eng *engine.Engine) http.HandlerFunc {
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
		stageID := reqData.StageID

		spec, _, _, err := Stages.Get(stageID)
		if err != nil {
			logrus.WithError(err).
				Errorln("failed to delete the stage")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		err = eng.Destroy(r.Context(), spec)
		if err != nil {
			logrus.WithError(err).
				Errorln("cannot destroy the docker environment")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, _ = Stages.Remove(stageID)

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
