package google

import (
	"context"
	"io/ioutil"
	"net/http"

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
	var client *http.Client
	var err error
	if prov.JsonPath != "" {
		prov.JSON, _ = ioutil.ReadFile(prov.JsonPath)
	}

	client, err = google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		panic(err)
	}
	computeService, err := compute.New(client)
	if err != nil {
		panic(err)
	}
	return computeService
}
