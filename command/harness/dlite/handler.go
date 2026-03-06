package dlite

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
	"github.com/wings-software/dlite/poller"

	"github.com/drone-runners/drone-runner-aws/command/harness"
)

// HTTP status codes and response constants.
var (
	okStatus       = "OK"
	enabledStatus  = "ENABLED"
	disabledStatus = "DISABLED"
)

// Handler creates the HTTP handler for dlite mode.
func Handler(p *poller.Poller, d *dliteCommand) http.Handler {
	r := chi.NewRouter()
	r.Use(harness.Middleware)
	r.Use(middleware.Recoverer)

	r.Mount("/maintenance_mode", maintenanceModeRouter(p, d))
	r.Mount("/metrics", promhttp.Handler())
	r.Get("/healthz", handleHealthz)

	return r
}

// maintenanceModeRouter creates the subrouter for maintenance mode endpoints.
func maintenanceModeRouter(p *poller.Poller, d *dliteCommand) http.Handler {
	sr := chi.NewRouter()
	sr.Get("/", handleStatus(p))
	sr.Post("/enable", handleEnable(p, d))
	sr.Post("/disable", handleDisable(p))
	return sr
}

// handleHealthz handles health check requests.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, okStatus) //nolint:errcheck
}

// handleEnable enables maintenance mode by filtering out new tasks.
func handleEnable(p *poller.Poller, d *dliteCommand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(func(ev *client.TaskEvent) bool {
			return ev.TaskType != initTask && ev.TaskType != executeTaskV2 && ev.TaskType != cleanupTaskV2
		})
		if err := d.runner.PoolManager.CleanPools(r.Context(), false, true); err != nil {
			io.WriteString(w, err.Error()) //nolint:errcheck
			return
		}
		io.WriteString(w, okStatus) //nolint:errcheck
	}
}

// handleDisable disables maintenance mode.
func handleDisable(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(nil)
		io.WriteString(w, okStatus) //nolint:errcheck
	}
}

// handleStatus returns the current maintenance mode status.
func handleStatus(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p.Filter == nil {
			io.WriteString(w, disabledStatus) //nolint:errcheck
			return
		}
		io.WriteString(w, enabledStatus) //nolint:errcheck
	}
}

// pollerMiddleware wraps task handlers with panic recovery.
func pollerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				err := fmt.Errorf("http: panic: %v", rec)
				logrus.WithError(err).Errorln("Panic occurred")
				httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
