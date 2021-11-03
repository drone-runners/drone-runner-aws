// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package poolfile

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/oshelp"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/sshkey"
	"gopkg.in/yaml.v2"
)

// PoolSettings defines default settings.
type PoolSettings struct {
	AwsAccessKeyID     string
	AwsAccessKeySecret string
	AwsRegion          string
	PrivateKeyFile     string
	PublicKeyFile      string
}

// helper function returns the base temporary directory based on the target platform.
func tempdir(inputOS string) string {
	dir := "aws"
	switch inputOS {
	case oshelp.WindowsString:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}

func compilePoolFile(rawPool engine.Pool, settings *PoolSettings) (engine.Pool, error) { //nolint:gocritic,gocyclo // its complex but standard
	pipelineOS := rawPool.Platform.OS
	// secrets and error here
	if rawPool.Account.AccessKeyID == "" {
		rawPool.Account.AccessKeyID = settings.AwsAccessKeyID
	}
	if rawPool.Account.AccessKeySecret == "" {
		rawPool.Account.AccessKeySecret = settings.AwsAccessKeySecret
	}
	// we need Access, error if its still empty
	if rawPool.Account.AccessKeyID == "" || rawPool.Account.AccessKeySecret == "" {
		return engine.Pool{}, fmt.Errorf("missing AWS access key or AWS secret. Add to .env file or pool file")
	}
	// try config first. then set the default region if not provided
	if rawPool.Account.Region == "" && settings.AwsRegion != "" {
		rawPool.Account.Region = settings.AwsRegion
	} else if rawPool.Account.Region == "" {
		rawPool.Account.Region = "us-east-1"
	}
	// set default instance type if not provided
	if rawPool.Instance.Type == "" {
		rawPool.Instance.Type = "t3.nano"
		if rawPool.Platform.Arch == "arm64" {
			rawPool.Instance.Type = "a1.medium"
		}
	}
	// put something into tags even if empty
	if rawPool.Instance.Tags == nil {
		rawPool.Instance.Tags = make(map[string]string)
	}
	// set the default disk size if not provided
	if rawPool.Instance.Disk.Size == 0 {
		rawPool.Instance.Disk.Size = 32
	}
	// set the default disk type if not provided
	if rawPool.Instance.Disk.Type == "" {
		rawPool.Instance.Disk.Type = "gp2"
	}
	// set the default iops
	if rawPool.Instance.Disk.Type == "io1" && rawPool.Instance.Disk.Iops == 0 {
		rawPool.Instance.Disk.Iops = 100
	}
	// set the default device
	if rawPool.Instance.Device.Name == "" {
		rawPool.Instance.Device.Name = "/dev/sda1"
	}
	// set the default ssh user. this user account is responsible for executing the pipeline script.
	switch {
	case rawPool.Instance.User == "" && rawPool.Platform.OS == oshelp.WindowsString:
		rawPool.Instance.User = "Administrator"
	case rawPool.Instance.User == "":
		rawPool.Instance.User = "root"
	}
	_, statErr := os.Stat(settings.PrivateKeyFile)
	if os.IsNotExist(statErr) {
		// there are no key files
		publickey, privatekey, generateKeyErr := sshkey.GeneratePair()
		if generateKeyErr != nil {
			publickey = ""
			privatekey = ""
		}
		rawPool.Instance.PrivateKey = privatekey
		rawPool.Instance.PublicKey = publickey
	} else {
		body, privateKeyErr := os.ReadFile(settings.PrivateKeyFile)
		if privateKeyErr != nil {
			log.Fatalf("unable to read file ``: %v", privateKeyErr)
		}
		rawPool.Instance.PrivateKey = string(body)

		body, publicKeyErr := os.ReadFile(settings.PublicKeyFile)
		if publicKeyErr != nil {
			log.Fatalf("unable to read file: %v", publicKeyErr)
		}
		rawPool.Instance.PublicKey = string(body)
	}
	// generate the cloudinit file
	var userDataWithSSH string
	if rawPool.Platform.OS == oshelp.WindowsString {
		userDataWithSSH = cloudinit.Windows(cloudinit.Params{
			PublicKey: rawPool.Instance.PublicKey,
		})
	} else {
		// try using cloud init.
		userDataWithSSH = cloudinit.Linux(cloudinit.Params{
			PublicKey: rawPool.Instance.PublicKey,
		})
	}
	rawPool.Instance.UserData = userDataWithSSH
	// create the root directory
	rawPool.Root = tempdir(pipelineOS)

	return rawPool, nil
}

func ProcessPoolFile(rawFile string, settings *PoolSettings) (foundPools map[string]engine.Pool, err error) {
	rawPool, readPoolFileErr := ioutil.ReadFile(rawFile)
	if readPoolFileErr != nil {
		errorMessage := fmt.Sprintf("unable to read file: %s", rawFile)
		return nil, fmt.Errorf(errorMessage, readPoolFileErr)
	}
	foundPools = make(map[string]engine.Pool)
	buf := bytes.NewBuffer(rawPool)
	dec := yaml.NewDecoder(buf)

	for {
		rawPool := new(engine.Pool)
		err := dec.Decode(rawPool)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		preppedPool, compilePoolFileErr := compilePoolFile(*rawPool, settings)
		if compilePoolFileErr != nil {
			return nil, compilePoolFileErr
		}
		foundPools[rawPool.Name] = preppedPool
	}
	return foundPools, nil
}
