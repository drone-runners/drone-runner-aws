package delegate

import (
	"encoding/json"
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
	Pool  string     `json:"pool"`
	Files []FileInfo `json:"files"`
	api.SetupRequest
}

type FileInfo struct {
	File File `json:"file"`
}

type File struct {
	Path  string `json:"path"`
	Mode  uint32 `json:"mode"`
	Data  string `json:"data"`
	IsDir bool   `json:"is_dir"`
}

func GetDestroyRequest(r io.Reader) (*DestroyRequest, error) {
	d := &DestroyRequest{}
	if err := getJSONDataFromReader(r, d); err != nil {
		return nil, err
	}

	return d, nil
}

type DestroyRequest struct {
	StageID string `json:"stage_id"`
	Pool    string `json:"pool"`
	ID      string `json:"id"`
}

func GetExecStepRequest(r io.Reader) (*ExecStepRequest, error) {
	d := &ExecStepRequest{}
	if err := getJSONDataFromReader(r, d); err != nil {
		return nil, err
	}

	return d, nil
}

type ExecStepRequest struct {
	IP     string
	StepID string
	api.StartStepRequest
}
