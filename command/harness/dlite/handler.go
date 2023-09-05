package dlite

import (
	"fmt"
	"io"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
	"github.com/wings-software/dlite/poller"
)

var (
	okStatus       = "OK"
	enabledStatus  = "ENABLED"
	disabledStatus = "DISABLED"
)

func Handler(p *poller.Poller, d *dliteCommand) http.Handler {
	r := chi.NewRouter()
	r.Use(harness.Middleware)
	r.Use(middleware.Recoverer)

	r.Mount("/maintenance_mode", func() http.Handler {
		sr := chi.NewRouter()
		sr.Get("/", handleStatus(p))
		sr.Post("/enable", handleEnable(p, d))
		sr.Post("/disable", handleDisable(p))
		return sr
	}())

	r.Mount("/metrics", promhttp.Handler())

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, okStatus) //nolint: errcheck
	})
	return r
}

func handleEnable(p *poller.Poller, d *dliteCommand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(func(ev *client.TaskEvent) bool {
			return ev.TaskType != initTask
		})
		err := d.poolManager.CleanPools(r.Context(), false, true)
		if derr := d.distributedPoolManager.CleanPools(r.Context(), false, true); derr != nil {
			err = derr
		}
		if err != nil {
			io.WriteString(w, err.Error()) //nolint: errcheck
		}
		io.WriteString(w, okStatus) //nolint: errcheck
	}
}

func handleDisable(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(nil)
		io.WriteString(w, okStatus) //nolint: errcheck
	}
}

func handleStatus(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p.Filter == nil {
			io.WriteString(w, disabledStatus) //nolint: errcheck
			return
		}
		io.WriteString(w, enabledStatus) //nolint: errcheck
	}
}

func pollerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				// Handle the panic here, log it, or respond with an error message.
				err := fmt.Errorf("http: panic: %v", r)
				logrus.WithError(err).Errorln("Panic occurred")
				httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
