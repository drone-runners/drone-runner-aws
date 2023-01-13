package tester

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/harness/lite-engine/api"
	"github.com/sirupsen/logrus"
)

type CleanupRequest struct {
	ID            string `json:"id"`
	InstanceID    string `json:"instance_id"`
	PoolID        string `json:"pool_id"`
	CorrelationID string `json:"correlation_id"`
}

// Error represents a json-encoded API error.
type Error struct {
	Message string
	Code    int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%s", e.Code, e.Message)
}

// HTTPClient provides an http service client.
type HTTPClient struct {
	Client   *http.Client
	Endpoint string
}

// Setup will setup the stage config
func (c *HTTPClient) Setup(ctx context.Context, in *harness.SetupVMRequest) (*harness.SetupVMResponse, error) {
	path := "/setup"
	out := new(harness.SetupVMResponse)
	_, err := c.do(ctx, c.Endpoint+path, http.MethodPost, in, out) //nolint:bodyclose
	return out, err
}

// Destroy will clean up the resources created
func (c *HTTPClient) Destroy(ctx context.Context, in *CleanupRequest) error {
	path := "/destroy"
	_, err := c.do(ctx, c.Endpoint+path, http.MethodPost, in, nil) //nolint:bodyclose
	return err
}

func (c *HTTPClient) Step(ctx context.Context, in *harness.ExecuteVMRequest) (*api.PollStepResponse, error) {
	path := "/step"
	out := new(api.PollStepResponse)
	_, err := c.do(ctx, c.Endpoint+path, http.MethodPost, in, out) //nolint:bodyclose
	return out, err
}

// do is a helper function that posts a http request with the input encoded and response decoded from json.
func (c *HTTPClient) do(ctx context.Context, path, method string, in, out interface{}) (*http.Response, error) { //nolint:unparam
	var r io.Reader

	if in != nil {
		buf := new(bytes.Buffer)
		if err := json.NewEncoder(buf).Encode(in); err != nil {
			logrus.WithError(err).Errorln("failed to encode input")
			return nil, err
		}
		r = buf
	}

	req, err := http.NewRequestWithContext(ctx, method, path, r)
	if err != nil {
		return nil, err
	}

	res, err := c.Client.Do(req)
	if res != nil {
		defer func() {
			// drain the response body so we can reuse
			// this connection.
			if _, cerr := io.Copy(io.Discard, io.LimitReader(res.Body, 4096)); cerr != nil { //nolint:gomnd
				logrus.WithError(cerr).Errorln("failed to drain response body")
			}
			res.Body.Close()
		}()
	}
	if err != nil {
		return res, err
	}

	// if the response body return no content we exit
	// immediately. We do not read or unmarshal the response
	// and we do not return an error.
	if res.StatusCode == http.StatusNoContent {
		return res, nil
	}

	// else read the response body into a byte slice.
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return res, err
	}

	if res.StatusCode > 299 { //nolint:gomnd
		// if the response body includes an error message
		// we should return the error string.
		if len(body) != 0 {
			out := new(struct {
				Message string `json:"error_msg"`
			})
			if err := json.Unmarshal(body, out); err == nil {
				return res, &Error{Code: res.StatusCode, Message: out.Message}
			}
			return res, &Error{Code: res.StatusCode, Message: string(body)}
		}
		// if the response body is empty we should return
		// the default status code text.
		return res, errors.New(
			http.StatusText(res.StatusCode),
		)
	}
	if out == nil {
		return res, nil
	}
	return res, json.Unmarshal(body, out)
}
