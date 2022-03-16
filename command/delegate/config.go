// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package delegate

import (
	"os"

	"github.com/kelseyhightower/envconfig"
)

// Config stores the system configuration.
type Config struct {
	Debug bool `envconfig:"DRONE_DEBUG"`
	Trace bool `envconfig:"DRONE_TRACE"`

	Server struct {
		Port  string `envconfig:"DRONE_HTTP_BIND" default:":3000"`
		Proto string `envconfig:"DRONE_HTTP_PROTO"`
		Host  string `envconfig:"DRONE_HTTP_HOST"`
		Acme  bool   `envconfig:"DRONE_HTTP_ACME"`
	}

	Runner struct {
		Name string `envconfig:"DRONE_RUNNER_NAME"`
	}

	Settings struct {
		LiteEnginePath string `envconfig:"DRONE_LITE_ENGINE_PATH" default:"https://github.com/harness/lite-engine/releases/download/v0.0.1.14/"`
		BusyMaxAge     int64  `envconfig:"DRONE_SETTINGS_BUSY_MAX_AGE" default:"2"`
		FreeMaxAge     int64  `envconfig:"DRONE_SETTINGS_FREE_MAX_AGE" default:"12"`
		ReusePool      bool   `envconfig:"DRONE_REUSE_POOL" default:"false"`
	}

	Database struct {
		Driver     string `envconfig:"APP_DATABASE_DRIVER" default:"sqlite3"`
		Datasource string `envconfig:"APP_DATABASE_DATASOURCE" default:"database.sqlite3"`
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
	if config.Runner.Name == "" {
		config.Runner.Name, _ = os.Hostname()
	}

	return config, nil
}
