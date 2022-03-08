package config

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/ghodss/yaml"
)

func ProcessPoolFile(rawFile string) (*PoolFile, error) {
	rawPool, err := os.ReadFile(rawFile)
	if err != nil {
		return nil, err
	}
	data := io.NopCloser(
		bytes.NewBuffer(rawPool),
	)
	inst, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return inst, nil
}

// Parse parses the configuration from io.Reader r.
func Parse(r io.Reader) (*PoolFile, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	b, err = yaml.YAMLToJSON(b)
	if err != nil {
		return nil, err
	}
	out := new(PoolFile)
	err = json.Unmarshal(b, out)
	return out, err
}
