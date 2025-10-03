package common

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/drone-runners/drone-runner-aws/types"
)

type InstanceInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IPAddress         string `json:"ip_address"`
	Port              int64  `json:"port"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	Provider          string `json:"provider"`
	PoolName          string `json:"pool_name"`
	Zone              string `json:"zone"`
	StorageIdentifier string `json:"storage_identifier"`
	CAKey             []byte `json:"ca_key"`
	CACert            []byte `json:"ca_cert"`
	TLSKey            []byte `json:"tls_key"`
	TLSCert           []byte `json:"tls_cert"`
	IsWarmed          bool   `json:"is_warmed"`
	IsHibernated      bool   `json:"is_hibernated"`
}

// ValidateStruct checks if all fields of a struct are populated.
func ValidateStruct(data interface{}) error {
	return ValidateStructForKeys(data, []string{})
}

// ValidateStructForKeys checks if the specified fields of a struct are populated.
// If keys are empty, it checks all fields.
func ValidateStructForKeys(data interface{}, keys []string) error {
	v := reflect.ValueOf(data)

	// Ensure the input is a struct
	if v.Kind() != reflect.Struct {
		return errors.New("input is not a struct")
	}

	checkAll := len(keys) == 0
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}

	// Iterate over the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := v.Type().Field(i)
		fieldName := fieldType.Name

		// Validate the field if we're checking all or it's in the keys list
		if checkAll || containsKey(keySet, fieldName) {
			if reflect.DeepEqual(field.Interface(), reflect.Zero(field.Type()).Interface()) {
				return fmt.Errorf("field %q is not populated", fieldName)
			}
		}
	}

	return nil
}

// containsKey checks if a field name is in the key set.
func containsKey(set map[string]struct{}, key string) bool {
	_, exists := set[key]
	return exists
}

func BuildInstanceFromRequest(instanceInfo InstanceInfo) *types.Instance { //nolint:gocritic
	return &types.Instance{
		ID:       instanceInfo.ID,
		Name:     instanceInfo.Name,
		Address:  instanceInfo.IPAddress,
		Provider: types.DriverType(instanceInfo.Provider),
		Pool:     instanceInfo.PoolName,
		Platform: types.Platform{
			OS:   instanceInfo.OS,
			Arch: instanceInfo.Arch,
		},
		IsHibernated:      instanceInfo.IsHibernated,
		IsWarmed:          instanceInfo.IsWarmed,
		Port:              instanceInfo.Port,
		Zone:              instanceInfo.Zone,
		StorageIdentifier: instanceInfo.StorageIdentifier,
		CAKey:             instanceInfo.CAKey,
		CACert:            instanceInfo.CACert,
		TLSCert:           instanceInfo.TLSCert,
		TLSKey:            instanceInfo.TLSKey,
	}
}
