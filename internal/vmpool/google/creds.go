package google

import (
	"context"
	"net/http"
	"os"

	"github.com/drone/runner-go/logger"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

type Credentials struct {
	ProjectID   string
	JSONPath    string
	TokenSource oauth2.TokenSource
	JSON        []byte
}

func (prov *Credentials) getService() *compute.Service {
	ctx := context.Background()
	logr := logger.FromContext(ctx).
		WithField("provider", provider).
		WithField("project", prov.ProjectID)

	var client *http.Client
	var err error
	if prov.JSONPath != "" {
		prov.JSON, _ = os.ReadFile(prov.JSONPath)
	}

	client, err = google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		logr.WithError(err).Errorln("unable to create google client")
	}

	computeService, err := compute.New(client) //nolint:staticcheck
	if err != nil {
		logr.WithError(err).Errorln("unable to create google client")
	}
	return computeService
}
