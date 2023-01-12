package dlite

import (
	"io"
	"net/http"

	"github.com/drone-runners/drone-runner-vm/command/harness"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/poller"
)

var (
	okStatus       = "OK"
	enabledStatus  = "ENABLED"
	disabledStatus = "DISABLED"
)

func Handler(p *poller.Poller) http.Handler {
	r := chi.NewRouter()
	r.Use(harness.Middleware)
	r.Use(middleware.Recoverer)

	r.Mount("/maintenance_mode", func() http.Handler {
		sr := chi.NewRouter()
		sr.Get("/", handleStatus(p))
		sr.Post("/enable", handleEnable(p))
		sr.Post("/disable", handleDisable(p))
		return sr
	}())

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, okStatus) //nolint: errcheck
	})
	return r
}

func handleEnable(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(func(ev *client.TaskEvent) bool {
			return ev.TaskType != initTask
		})
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
