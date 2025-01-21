package harness

import (
	"errors"
	"fmt"
	"github.com/drone-runners/drone-runner-aws/types"
	"hash/fnv"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"

	leapi "github.com/harness/lite-engine/api"
	lelivelog "github.com/harness/lite-engine/livelog"
	lestream "github.com/harness/lite-engine/logstream/remote"
)

func getStreamLogger(cfg leapi.LogConfig, mtlsConfig spec.MtlsConfig, logKey, correlationID string) *lelivelog.Writer {
	client := lestream.NewHTTPClient(cfg.URL, cfg.AccountID,
		cfg.Token, cfg.IndirectUpload, false, mtlsConfig.ClientCert, mtlsConfig.ClientCertKey)
	wc := lelivelog.New(client, logKey, correlationID, nil, true, cfg.TrimNewLineSuffix)
	go func() {
		if err := wc.Open(); err != nil {
			logrus.WithError(err).Debugln("failed to open log stream")
		}
	}()
	return wc
}

// generate a id from the filename
// /path/to/a.txt and /other/path/to/a.txt should generate different hashes
// eg - a-txt10098 and a-txt-270089
func fileID(filename string) string {
	h := fnv.New32a()
	h.Write([]byte(filename))
	return strings.Replace(filepath.Base(filename), ".", "-", -1) + strconv.Itoa(int(h.Sum32()))
}

// validateStruct checks if all fields of a struct are populated.
func validateStruct(data interface{}) error {
	v := reflect.ValueOf(data)

	// Ensure the input is a struct
	if v.Kind() != reflect.Struct {
		return errors.New("input is not a struct")
	}

	// Iterate over the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := v.Type().Field(i)

		// Check for zero values
		if reflect.DeepEqual(field.Interface(), reflect.Zero(field.Type()).Interface()) {
			return fmt.Errorf("field %q is not populated", fieldType.Name)
		}
	}

	return nil
}

func buildInstanceFromRequest(instanceInfo InstanceInfo) *types.Instance {
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
		IsHibernated: false,
		Port:         instanceInfo.Port,
		Zone:         instanceInfo.Zone,
	}
}
