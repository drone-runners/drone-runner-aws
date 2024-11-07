package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

type PoolMap map[string]string
type PoolMapperByAccount map[string]PoolMap

func (pma *PoolMapperByAccount) Decode(value string) error {
	m := map[string]PoolMap{}
	pairs := strings.Split(value, ";")
	for _, pair := range pairs {
		p := PoolMap{}
		kvpair := strings.Split(pair, "=")
		if len(kvpair) != 2 { //nolint:gomnd
			return fmt.Errorf("invalid map item: %q", pair)
		}
		err := json.Unmarshal([]byte(kvpair[1]), &p)
		if err != nil {
			return fmt.Errorf("invalid map json: %w", err)
		}
		m[kvpair[0]] = p
	}
	*pma = PoolMapperByAccount(m)
	return nil
}

// Iterates over all the entries and converts it to a simple type
func (pma *PoolMapperByAccount) Convert() map[string]map[string]string {
	m := map[string]map[string]string{}
	for k, v := range *pma {
		m[k] = map[string]string(v)
	}
	return m
}
