// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package daemon

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config stores the system configuration.
type Config struct {
	Debug bool `envconfig:"DRONE_DEBUG"`
	Trace bool `envconfig:"DRONE_TRACE"`

	Client struct {
		Address    string `ignored:"true"`
		Proto      string `envconfig:"DRONE_RPC_PROTO"  default:"http"`
		Host       string `envconfig:"DRONE_RPC_HOST" required:"true"`
		Secret     string `envconfig:"DRONE_RPC_SECRET" required:"true"`
		SkipVerify bool   `envconfig:"DRONE_RPC_SKIP_VERIFY"`
		Dump       bool   `envconfig:"DRONE_RPC_DUMP_HTTP"`
		DumpBody   bool   `envconfig:"DRONE_RPC_DUMP_HTTP_BODY"`
	}

	Dashboard struct {
		Disabled bool   `envconfig:"DRONE_UI_DISABLE"`
		Username string `envconfig:"DRONE_UI_USERNAME"`
		Password string `envconfig:"DRONE_UI_PASSWORD"`
		Realm    string `envconfig:"DRONE_UI_REALM" default:"MyRealm"`
	}

	Server struct {
		Port  string `envconfig:"DRONE_HTTP_BIND" default:":3000"`
		Proto string `envconfig:"DRONE_HTTP_PROTO"`
		Host  string `envconfig:"DRONE_HTTP_HOST"`
		Acme  bool   `envconfig:"DRONE_HTTP_ACME"`
	}

	Runner struct {
		Name        string            `envconfig:"DRONE_RUNNER_NAME"`
		Capacity    int               `envconfig:"DRONE_RUNNER_CAPACITY" default:"6"`
		Procs       int64             `envconfig:"DRONE_RUNNER_MAX_PROCS"`
		Environ     map[string]string `envconfig:"DRONE_RUNNER_ENVIRON"`
		EnvFile     string            `envconfig:"DRONE_RUNNER_ENV_FILE"`
		Secrets     map[string]string `envconfig:"DRONE_RUNNER_SECRETS"`
		Labels      map[string]string `envconfig:"DRONE_RUNNER_LABELS"`
		NetworkOpts map[string]string `envconfig:"DRONE_RUNNER_NETWORK_OPTS"`
		Volumes     []string          `envconfig:"DRONE_RUNNER_VOLUMES"`
	}

	Limit struct {
		Repos   []string `envconfig:"DRONE_LIMIT_REPOS"`
		Events  []string `envconfig:"DRONE_LIMIT_EVENTS"`
		Trusted bool     `envconfig:"DRONE_LIMIT_TRUSTED"`
	}

	Settings struct {
		LiteEnginePath string `envconfig:"DRONE_LITE_ENGINE_PATH" default:"https://github.com/harness/lite-engine/releases/download/v0.0.1.14/"`
		ReusePool      bool   `envconfig:"DRONE_REUSE_POOL" default:"false"`
	}

	Environ struct {
		Endpoint   string `envconfig:"DRONE_ENV_PLUGIN_ENDPOINT"`
		Token      string `envconfig:"DRONE_ENV_PLUGIN_TOKEN"`
		SkipVerify bool   `envconfig:"DRONE_ENV_PLUGIN_SKIP_VERIFY"`
	}

	Secret struct {
		Endpoint   string `envconfig:"DRONE_SECRET_PLUGIN_ENDPOINT"`
		Token      string `envconfig:"DRONE_SECRET_PLUGIN_TOKEN"`
		SkipVerify bool   `envconfig:"DRONE_SECRET_PLUGIN_SKIP_VERIFY"`
	}

	Docker struct {
		Config string `envconfig:"DRONE_DOCKER_CONFIG"`
		Stream bool   `envconfig:"DRONE_DOCKER_STREAM_PULL" default:"true"` // TODO: Currently unused
	}

	Registry struct {
		Endpoint   string `envconfig:"DRONE_REGISTRY_PLUGIN_ENDPOINT"`
		Token      string `envconfig:"DRONE_REGISTRY_PLUGIN_TOKEN"`
		SkipVerify bool   `envconfig:"DRONE_REGISTRY_PLUGIN_SKIP_VERIFY"`
	}

	Database struct {
		Driver     string `envconfig:"DRONE_DATABASE_DRIVER" default:"sqlite3"`
		Datasource string `envconfig:"DRONE_DATABASE_DATASOURCE" default:"database.sqlite3"`
	}
}

// legacy environment variables. the key is the legacy
// variable name, and the value is the new variable name.
var legacy = map[string]string{
	// "DRONE_VARIABLE_OLD": "DRONE_VARIABLE_NEW"
}

func fromEnviron() (Config, error) {
	// loop through legacy environment variable and, if set
	// rewrite to the new variable name.
	for k, v := range legacy {
		if s, ok := os.LookupEnv(k); ok {
			os.Setenv(v, s)
		}
	}

	var config Config
	err := envconfig.Process("", &config)
	if err != nil {
		return config, err
	}
	if config.Runner.Environ == nil {
		config.Runner.Environ = map[string]string{}
	}
	if config.Runner.Name == "" {
		config.Runner.Name, _ = os.Hostname()
	}
	if config.Dashboard.Password == "" {
		config.Dashboard.Disabled = true
	}
	config.Client.Address = fmt.Sprintf(
		"%s://%s",
		config.Client.Proto,
		config.Client.Host,
	)

	// environment variables can be sourced from a separate
	// file. These variables are loaded and appended to the
	// environment list.
	if file := config.Runner.EnvFile; file != "" {
		envs, err := godotenv.Read(file)
		if err != nil {
			return config, err
		}
		for k, v := range envs {
			config.Runner.Environ[k] = v
		}
	}

	return config, nil
}
