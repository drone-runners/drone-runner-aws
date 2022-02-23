package google

import (
	"context"
	"io/ioutil"
	"net/http"

	"github.com/drone/runner-go/logger"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

type Credentials struct {
	ProjectID   string
	JsonPath    string
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
	if prov.JsonPath != "" {
		prov.JSON, _ = ioutil.ReadFile(prov.JsonPath)
	}

	client, err = google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		logr.WithError(err).Errorln("unable to create google client")
	}
	computeService, err := compute.New(client)
	if err != nil {
		logr.WithError(err).Errorln("unable to create google client")
	}
	return computeService
}
