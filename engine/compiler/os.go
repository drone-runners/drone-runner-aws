// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/buildkite/yaml"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/shell/bash"
	"github.com/drone/runner-go/shell/powershell"
	json "github.com/ghodss/yaml"
)

// helper function returns the base temporary directory based
// on the target platform.
func tempdir(os string) string {
	dir := "aws"
	switch os {
	case windowsString:
		return join(os, "C:\\Windows\\Temp", dir)
	default:
		return join(os, "/tmp", dir)
	}
}

// helper function joins the file paths.
func join(os string, paths ...string) string {
	switch os {
	case windowsString:
		return strings.Join(paths, "\\")
	default:
		return strings.Join(paths, "/")
	}
}

// helper function returns the shell extension based on the
// target platform.
func getExt(os, file string) (s string) {
	switch os {
	case windowsString:
		return file + ".ps1"
	default:
		return file
	}
}

// helper function returns the shell command and arguments
// based on the target platform to invoke the script
func getCommand(os, script string) (cmd string, args []string) {
	cmd, args = bash.Command()
	if os == windowsString {
		cmd, args = powershell.Command()
	}
	return cmd, append(args, script)
}

// helper function returns the netrc file name based on the target platform.
func getNetrc(os string) string {
	switch os {
	case windowsString:
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
	case windowsString:
		return powershell.Script(commands)
	default:
		return bash.Script(commands)
	}
}

func convertEnvMapToString(env map[string]string) (envString string) {
	for key, value := range env {
		if value == "" {
			continue
		}
		s := fmt.Sprintf(" --env %s='%s'", key, value)
		envString += (s)
	}
	return envString
}

func convertSettingsToString(settings map[string]*manifest.Parameter) (envString string) {
	for key, value := range settings {
		// fix https://github.com/drone/drone-yaml/issues/13
		if value == nil {
			continue
		}
		// all settings are passed to the plugin env variables, prefixed with PLUGIN_
		key = "PLUGIN_" + strings.ToUpper(key)
		// if the setting parameter is sources from the secret we create a secret environment variable.
		if value.Secret != "" {
			s := fmt.Sprintf(" --env %s='%s'", key, value.Secret)
			envString += (s)
		} else {
			// else if the setting parameter is opaque  we inject as a string-encoded environment variable.
			s := fmt.Sprintf(" --env %s='%s'", key, encode(value.Value))
			envString += (s)
		}
	}
	return envString
}

func convertVolumesToString(pipelineOS, sourcedir string, stepVolumes []*resource.VolumeMount, pipeLineVolumeMap map[string]string) (volumeString string) {
	if pipelineOS == windowsString {
		volumeString = fmt.Sprintf("-v `%s`:c:/drone/src", sourcedir)
	} else {
		volumeString = fmt.Sprintf(`-v '%s':/drone/src`, sourcedir)
	}
	for _, volume := range stepVolumes {
		path, match := pipeLineVolumeMap[volume.Name]
		if match {
			v := fmt.Sprintf(` -v '%s':%s`, path, volume.MountPath)
			if pipelineOS == windowsString {
				v = fmt.Sprintf(" -v `%s`:%s", path, volume.MountPath)
			}
			volumeString += v
		}
	}
	return volumeString
}

func convertStepNametoContainerString(stepName string) (containerName string) {
	// name of the container
	reg := regexp.MustCompile("[^a-zA-Z0-9_.-]+")
	safeName := reg.ReplaceAllString(stepName, "")
	containerName = fmt.Sprintf(`--name='%s'`, safeName)
	return containerName
}

func convertCommandsToEntryPointString(pipelineOS string, commands []string) (entryPoint string) {
	if len(commands) > 0 {
		for i := range commands {
			entryPoint = fmt.Sprintf(`%s %s;`, entryPoint, commands[i])
		}
		if pipelineOS == windowsString {
			entryPoint = fmt.Sprintf(`powershell '%s'`, entryPoint)
		} else {
			entryPoint = fmt.Sprintf(`/bin/bash -c %q`, entryPoint)
		}
	}
	return entryPoint
}

func genDockerCommandLine(pipelineOS, sourcedir string, step *resource.Step, env, pipeLineVolumeMap map[string]string) string {
	// create the env params to be passed to the docker executable
	envString := convertEnvMapToString(env)
	// convert settings to env variables
	envString += convertSettingsToString(step.Settings)
	// mount the source dir
	volumeString := convertVolumesToString(pipelineOS, sourcedir, step.Volumes, pipeLineVolumeMap)
	// detached or interactive
	interactiveDeamonString := "--tty"
	if step.Detach {
		interactiveDeamonString = "--detach"
	}
	// container name
	containerName := convertStepNametoContainerString(step.Name)
	// networking
	networkString := `--network='myNetwork'`
	// if we are executing commands (plural), build docker command lines
	entryPoint := convertCommandsToEntryPointString(pipelineOS, step.Commands)
	commandBase := ""
	switch pipelineOS {
	case windowsString:
		commandBase = fmt.Sprintf("docker run %s -w='c:/drone/src' %s %s %s %s %s %s", interactiveDeamonString, containerName, networkString, volumeString, envString, step.Image, entryPoint)
	default:
		// -w set working dir, relies on the sourcedir being mounted
		commandBase = fmt.Sprintf("docker run %s --privileged -w='/drone/src' %s %s %s %s %s %s", interactiveDeamonString, containerName, networkString, volumeString, envString, step.Image, entryPoint)
	}
	switch pipelineOS {
	case windowsString:
		array := append([]string{}, commandBase)
		base := powershell.Script(array)
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
