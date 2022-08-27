package dlite

import (
	"io"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/go-chi/chi"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/poller"
)

func Handler(p *poller.Poller) http.Handler {
	r := chi.NewRouter()
	r.Use(harness.Middleware)

	r.Mount("/maintenance_mode", func() http.Handler {
		sr := chi.NewRouter()
		sr.Post("/enable", handleEnable(p))
		sr.Post("/disable", handleDisable(p))
		return sr
	}())
	return r
}

func handleEnable(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(func(ev *client.TaskEvent) bool {
			if ev.TaskType != initTask {
				return true
			}
			return false
		})
		io.WriteString(w, "OK")
	}
}

func handleDisable(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.SetFilter(nil)
		io.WriteString(w, "OK")
	}
}
