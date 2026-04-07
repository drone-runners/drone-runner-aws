package harness

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrap := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		reqStart := time.Now().UTC()
		next.ServeHTTP(wrap, r)

		status := wrap.Status()
		dur := time.Since(reqStart).Milliseconds()

		logr := logrus.WithContext(r.Context()).
			WithField("t", reqStart.Format(time.RFC3339)).
			WithField("status", status).
			WithField("dur[ms]", dur)
		logLine := "HTTP: " + r.Method + " " + r.URL.RequestURI()
		// Avoid logging health checks and metrics scrapes to avoid spamming the logs
		if strings.Contains(r.URL.RequestURI(), "healthz") || strings.Contains(r.URL.RequestURI(), "/metrics") {
			return
		}
		if status >= http.StatusInternalServerError {
			logr.Errorln(logLine)
		} else if strings.Contains(r.URL.RequestURI(), "pool_owner") {
			logr.Debugln(logLine)
		} else {
			logr.Infoln(logLine)
		}
	})
}
