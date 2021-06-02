// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/buildkite/yaml"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone/runner-go/shell/bash"
	"github.com/drone/runner-go/shell/powershell"
	json "github.com/ghodss/yaml"
)

// helper function returns the base temporary directory based
// on the target platform.
func tempdir(os string) string {
	dir := fmt.Sprintf("drone-%s", random())
	switch os {
	case "windows":
		return join(os, "C:\\Windows\\Temp", dir)
	default:
		return join(os, "/tmp", dir)
	}
}

// helper function joins the file paths.
func join(os string, paths ...string) string {
	switch os {
	case "windows":
		return strings.Join(paths, "\\")
	default:
		return strings.Join(paths, "/")
	}
}

// helper function returns the shell extension based on the
// target platform.
func getExt(os, file string) (s string) {
	switch os {
	case "windows":
		return file + ".ps1"
	default:
		return file
	}
}

// helper function returns the shell command and arguments
// based on the target platform to invoke the script
func getCommand(os, script string) (string, []string) {
	cmd, args := bash.Command()
	switch os {
	case "windows":
		cmd, args = powershell.Command()
	}
	return cmd, append(args, script)
}

// helper function returns the netrc file name based on the target platform.
func getNetrc(os string) string {
	switch os {
	case "windows":
		return "_netrc"
	default:
		return ".netrc"
	}
}

// helper function generates and returns a shell script to
// execute the provided shell commands. The shell scripting
// language (bash vs powershell) is determined by the operating
// system.
func genScript(os string, commands []string) string {
	switch os {
	case "windows":
		return powershell.Script(commands)
	default:
		return bash.Script(commands)
	}
}

func genDockerScript(os, sourcedir string, step *resource.Step, env map[string]string, pipeLineVolumeMap map[string]string) string {
	// create the env params to be passed to the docker executable
	envString := ""
	for key, value := range env {
		if value == "" {
			continue
		}
		s := fmt.Sprintf(" --env %s=\"%s\"", key, value)
		envString = envString + (s)
	}
	// convert settings to env variables
	for key, value := range step.Settings {
		// fix https://github.com/drone/drone-yaml/issues/13
		if value == nil {
			continue
		}
		// all settings are passed to the plugin env
		// variables, prefixed with PLUGIN_
		key = "PLUGIN_" + strings.ToUpper(key)

		// if the setting parameter is sources from the
		// secret we create a secret enviornment variable.
		if value.Secret != "" {
			s := fmt.Sprintf(" --env %s=\"%s\"", key, value.Secret)
			envString = envString + (s)
		} else {
			// else if the setting parameter is opaque
			// we inject as a string-encoded environment
			// variable.
			s := fmt.Sprintf(" --env %s=\"%s\"", key, encode(value.Value))
			envString = envString + (s)
		}
	}
	// mount the source dir
	volumeString := fmt.Sprintf(`-v "%s":/drone/src`, sourcedir)
	// mount volumes
	for _, volume := range step.Volumes {
		path, match := pipeLineVolumeMap[volume.Name]
		if match {
			v := fmt.Sprintf(` -v "%s":%s`, path, volume.MountPath)
			volumeString = volumeString + v
		}
	}
	// detached or interactive
	interactiveDeamonString := "--tty"
	if step.Detach {
		interactiveDeamonString = "--detach"
	}
	// name of the container
	reg, err := regexp.Compile("[^a-zA-Z0-9_.-]+")
	if err != nil {
		log.Fatal(err)
	}
	safeName := reg.ReplaceAllString(step.Name, "")
	containerName := fmt.Sprintf(`--name='%s'`, safeName)
	// networking
	networkString := "--network='myNetwork'"
	// if we are executing commands (plural), build docker command lines
	entryPoint := ""
	if len(step.Commands) > 0 {
		for i := range step.Commands {
			entryPoint = fmt.Sprintf(`%s %s;`, entryPoint, step.Commands[i])
		}
		entryPoint = fmt.Sprintf(`/bin/bash -c "%s"`, entryPoint)
	}
	commandBase := ""
	switch os {
	case "windows":
		commandBase = powershell.Script(step.Commands)
	default:
		// -w set working dir, relies on the sourcedir being mounted
		commandBase = fmt.Sprintf("docker run %s --privileged -w='/drone/src' %s %s %s %s %s %s", interactiveDeamonString, containerName, networkString, volumeString, envString, step.Image, entryPoint)
	}
	switch os {
	case "windows":
		base := powershell.Script(step.Commands)
		return base
	default:
		array := append([]string{}, commandBase)
		returnVal := bash.Script(array)
		return returnVal
	}

}

func encode(v interface{}) string {
	switch v := v.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case []byte:
		return base64.StdEncoding.EncodeToString(v)
	case []interface{}:
		return encodeSlice(v)
	default:
		return encodeMap(v)
	}
}

// helper function encodes a parameter in map format.
func encodeMap(v interface{}) string {
	yml, _ := yaml.Marshal(v)
	out, _ := json.YAMLToJSON(yml)
	return string(out)
}

// helper function encodes a parameter in slice format.
func encodeSlice(v interface{}) string {
	out, _ := yaml.Marshal(v)

	in := []string{}
	err := yaml.Unmarshal(out, &in)
	if err == nil {
		return strings.Join(in, ",")
	}
	out, _ = json.YAMLToJSON(out)
	return string(out)
}
