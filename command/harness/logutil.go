package harness

import (
	"context"
	"fmt"
	"io"
	"os"

	leapi "github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/harness/lite-engine/logger"
	"github.com/sirupsen/logrus"
)

// ANSI color helpers for build (streamed) logs
const (
	ColorReset  = "\u001b[0m"
	ColorGreen  = "\u001b[32m"
	ColorYellow = "\u001b[33m"
	ColorCyan   = "\u001b[36m"
	ColorRed    = "\u001b[31m"
)

// ConfigureLogging sets up logging for either streamed build logs (when cfg.URL is set)
// or dlite service logs (when cfg.URL is empty). It returns an updated context carrying
// the log entry for lite-engine internals, the base logger, the contextual entry,
// an optional closer for streamed logs, and whether streaming is enabled.
func ConfigureLogging(
	ctx context.Context,
	cfg leapi.LogConfig,
	mtls lespec.MtlsConfig,
	logKey, correlationID, stageRuntimeID string,
) (context.Context, *logrus.Logger, *logrus.Entry, io.Closer, bool) {
	log := logrus.New()
	var (
		entry     *logrus.Entry
		streaming bool
		closer    io.Closer
	)

	if cfg.URL == "" {
		// dlite service logs
		log.Out = os.Stdout
		log.SetLevel(logrus.TraceLevel)
		entry = log.WithField("api", "dlite:setup").
			WithField("correlationID", correlationID).
			WithField("stage_runtime_id", stageRuntimeID)
		ctx = logger.WithContext(ctx, entry)
	} else {
		// streamed build logs
		wc := getStreamLogger(cfg, mtls, logKey, correlationID)
		log.Out = wc
		log.SetLevel(logrus.InfoLevel)
		entry = log.WithField("stage_runtime_id", stageRuntimeID)
		ctx = logger.WithContext(ctx, entry)
		closer = wc
		streaming = true
	}

	return ctx, log, entry, closer, streaming
}

// NewBuildLogPrinter returns a function that writes user-facing lines to the
// streamed build logs only when streaming is enabled.
func NewBuildLogPrinter(out io.Writer, streaming bool) func(string) {
	return func(message string) {
		if streaming {
			fmt.Fprintln(out, message)
		}
	}
}
