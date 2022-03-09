package config

import (
	"encoding/json"
	"io"
	"os"

	"github.com/ghodss/yaml"
)

func ParseFile(rawFile string) (*PoolFile, error) {
	f, err := os.Open(rawFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	inst, err := Parse(f)
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
