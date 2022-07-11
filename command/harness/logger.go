package harness

import (
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone/runner-go/logger"
	"github.com/sirupsen/logrus"
)

// helper function configures the global logger from
// the loaded configuration.
func SetupLogger(c *config.EnvConfig) {
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
