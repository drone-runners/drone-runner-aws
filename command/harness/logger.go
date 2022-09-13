package harness

import (
	"bytes"
	"os"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone/runner-go/logger"
	"github.com/sirupsen/logrus"
)

// Get stackdriver to display logs correctly
// https://github.com/sirupsen/logrus/issues/403
type OutputSplitter struct{}

func (splitter *OutputSplitter) Write(p []byte) (n int, err error) {
	if bytes.Contains(p, []byte("level=error")) {
		return os.Stderr.Write(p)
	}
	return os.Stdout.Write(p)
}

// helper function configures the global logger from
// the loaded configuration.
func SetupLogger(c *config.EnvConfig) {
	logrus.SetOutput(&OutputSplitter{})
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
