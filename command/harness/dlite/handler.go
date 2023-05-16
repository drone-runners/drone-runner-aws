package dlite

import (
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"io"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/poller"
)

var (
	okStatus       = "OK"
	enabledStatus  = "ENABLED"
	disabledStatus = "DISABLED"
)

func Handler(p *poller.Poller, manager *drivers.Manager) http.Handler {
	r := chi.NewRouter()
	r.Use(harness.Middleware)
	r.Use(middleware.Recoverer)

	r.Mount("/maintenance_mode", func() http.Handler {
		sr := chi.NewRouter()
		sr.Get("/", handleStatus(p))
		sr.Post("/enable", handleEnable(p, manager))
		sr.Post("/disable", handleDisable(p))
		return sr
	}())

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, okStatus) //nolint: errcheck
	})
	return r
}

func handleEnable(p *poller.Poller, manager *drivers.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(func(ev *client.TaskEvent) bool {
			return ev.TaskType != initTask
		})
		err := manager.CleanPools(r.Context(), false, true)
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
