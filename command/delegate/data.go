package delegate

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/harness/lite-engine/api"
)

func getJSONDataFromReader(r io.Reader, data interface{}) error {
	err := json.NewDecoder(r).Decode(data)
	if err != nil {
		return err
	}

	return nil
}

func GetSetupRequest(r io.Reader) (*SetupRequest, error) {
	d := &SetupRequest{}
	if err := getJSONDataFromReader(r, d); err != nil {
		return nil, err
	}

	return d, nil
}

type SetupRequest struct {
	CorrelationID    string            `json:"correlation_id"`
	PoolID           string            `json:"pool_id"`
	Tags             map[string]string `json:"tags"`
	api.SetupRequest `json:"setup_request"`
}

func GetDestroyRequest(r io.Reader) (*DestroyRequest, error) {
	d := &DestroyRequest{}
	if err := getJSONDataFromReader(r, d); err != nil {
		return nil, err
	}

	if d.PoolID == "" {
		return nil, errors.New("missing pool ID")
	}

	if d.InstanceID == "" && d.CorrelationID == "" {
		return nil, errors.New("requires either instance ID or correlation ID")
	}

	return d, nil
}

type DestroyRequest struct {
	CorrelationID string `json:"correlation_id"`
	PoolID        string `json:"pool_id"`
	InstanceID    string `json:"instance_id"`
}

func GetExecStepRequest(r io.Reader) (*ExecStepRequest, error) {
	d := &ExecStepRequest{}
	if err := getJSONDataFromReader(r, d); err != nil {
		return nil, err
	}

	return d, nil
}

type ExecStepRequest struct {
	CorrelationID        string `json:"correlation_id"`
	PoolID               string `json:"pool_id"`
	InstanceID           string `json:"instance_id"`
	IPAddress            string `json:"ip_address"`
	api.StartStepRequest `json:"start_step_request"`
}
