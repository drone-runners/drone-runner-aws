// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"reflect"
	"testing"

	"github.com/dchest/uniuri"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/shell/bash"
	"github.com/drone/runner-go/shell/powershell"
)

func Test_tempdir(t *testing.T) {
	// replace the default random function with one that
	// is deterministic, for testing purposes.
	random = notRandom

	// restore the default random function and the previously
	// specified temporary directory
	defer func() {
		random = uniuri.New
	}()

	tests := []struct {
		os   string
		path string
	}{
		{os: "windows", path: "C:\\Windows\\Temp\\aws"},
		{os: "linux", path: "/tmp/aws"},
		{os: "openbsd", path: "/tmp/aws"},
		{os: "netbsd", path: "/tmp/aws"},
		{os: "freebsd", path: "/tmp/aws"},
	}

	for _, test := range tests {
		if got, want := tempdir(test.os), test.path; got != want {
			t.Errorf("Want tempdir %s, got %s", want, got)
		}
	}
}

func Test_join(t *testing.T) {
	tests := []struct {
		os string
		a  []string
		b  string
	}{
		{os: "windows", a: []string{"C:", "Windows", "Temp"}, b: "C:\\Windows\\Temp"},
		{os: "linux", a: []string{"/tmp", "foo", "bar"}, b: "/tmp/foo/bar"},
	}
	for _, test := range tests {
		if got, want := join(test.os, test.a...), test.b; got != want {
			t.Errorf("Want %s, got %s", want, got)
		}
	}
}

func Test_getExt(t *testing.T) {
	tests := []struct {
		os string
		a  string
		b  string
	}{
		{os: "windows", a: "clone", b: "clone.ps1"},
		{os: "linux", a: "clone", b: "clone"},
	}
	for _, test := range tests {
		if got, want := getExt(test.os, test.a), test.b; got != want {
			t.Errorf("Want %s, got %s", want, got)
		}
	}
}

func Test_getCommand(t *testing.T) {
	cmd, args := getCommand("linux", "clone.sh")
	if got, want := cmd, "/bin/sh"; got != want {
		t.Errorf("Want command %s, got %s", want, got)
	}
	if !reflect.DeepEqual(args, []string{"-e", "clone.sh"}) {
		t.Errorf("Unexpected args %v", args)
	}

	cmd, args = getCommand("windows", "clone.ps1")
	if got, want := cmd, "powershell"; got != want {
		t.Errorf("Want command %s, got %s", want, got)
	}
	if !reflect.DeepEqual(args, []string{"-noprofile", "-noninteractive", "-command", "clone.ps1"}) {
		t.Errorf("Unexpected args %v", args)
	}
}

func Test_getNetrc(t *testing.T) {
	tests := []struct {
		os   string
		name string
	}{
		{os: "windows", name: "_netrc"},
		{os: "linux", name: ".netrc"},
		{os: "openbsd", name: ".netrc"},
		{os: "netbsd", name: ".netrc"},
		{os: "freebsd", name: ".netrc"},
	}
	for _, test := range tests {
		if got, want := getNetrc(test.os), test.name; got != want {
			t.Errorf("Want %s on %s, got %s", want, test.os, got)
		}
	}
}

func Test_getScript(t *testing.T) {
	commands := []string{"go build"}

	a := genScript("windows", commands)
	b := powershell.Script(commands)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Generated windows linux script")
	}

	a = genScript("linux", commands)
	b = bash.Script(commands)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Generated invalid linux script")
	}
}

func Test_convertEnvMapToString(t *testing.T) {
	tests := []struct {
		name          string
		args          map[string]string
		wantEnvString string
	}{
		{
			name:          "one var",
			args:          map[string]string{"DRONE_BRANCH": "main"},
			wantEnvString: ` --env DRONE_BRANCH='main'`,
		},
		{
			name:          "empty var",
			args:          map[string]string{"DRONE_BRANCH": ""},
			wantEnvString: ``,
		},
		{
			name:          "multiple vars",
			args:          map[string]string{"DRONE_BRANCH": "main", "DRONE_BUILD": "5"},
			wantEnvString: ` --env DRONE_BRANCH='main' --env DRONE_BUILD='5'`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotEnvString := convertEnvMapToString(tt.args); gotEnvString != tt.wantEnvString {
				t.Errorf("convertEnvMapToString() = #%s#, want #%s#", gotEnvString, tt.wantEnvString)
			}
		})
	}
}

func Test_convertSettingsToString(t *testing.T) {
	tests := []struct {
		name          string
		args          map[string]*manifest.Parameter
		wantEnvString string
	}{
		{
			name:          "one value",
			args:          map[string]*manifest.Parameter{"setting_a": {Value: 7}},
			wantEnvString: ` --env PLUGIN_SETTING_A='7'`,
		},
		{
			name:          "one secret",
			args:          map[string]*manifest.Parameter{"setting_b": {Secret: "bla"}},
			wantEnvString: ` --env PLUGIN_SETTING_B='bla'`,
		},
		{
			name:          "one secret, one value",
			args:          map[string]*manifest.Parameter{"setting_a": {Value: 7}, "setting_b": {Secret: "bla"}},
			wantEnvString: ` --env PLUGIN_SETTING_A='7' --env PLUGIN_SETTING_B='bla'`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotEnvString := convertSettingsToString(tt.args); gotEnvString != tt.wantEnvString {
				t.Errorf("convertSettingsToString() = #%v#, want #%v#", gotEnvString, tt.wantEnvString)
			}
		})
	}
}

func Test_convertVolumesToString(t *testing.T) {
	type args struct {
		pipelineOS        string
		sourcedir         string
		stepVolumes       []*resource.VolumeMount
		pipeLineVolumeMap map[string]string
	}
	tests := []struct {
		name             string
		args             args
		wantVolumeString string
	}{
		{
			name: "linux: only sourcedir",
			args: args{
				pipelineOS:        "linux",
				sourcedir:         "/bla",
				stepVolumes:       []*resource.VolumeMount{},
				pipeLineVolumeMap: map[string]string{},
			},
			wantVolumeString: `-v '/bla':/drone/src`,
		},
		{
			name: "linux: sourcedir, empty step volume",
			args: args{
				pipelineOS:        "linux",
				sourcedir:         "/bla",
				stepVolumes:       []*resource.VolumeMount{},
				pipeLineVolumeMap: map[string]string{"cache": "/some/path"},
			},
			wantVolumeString: `-v '/bla':/drone/src`,
		},
		{
			name: "linux: sourcedir, step volume and volumeMap",
			args: args{
				pipelineOS:        "linux",
				sourcedir:         "/bla",
				stepVolumes:       []*resource.VolumeMount{{Name: "cache", MountPath: "/container/mount/point"}},
				pipeLineVolumeMap: map[string]string{"cache": "/host/some/path"},
			},
			wantVolumeString: `-v '/bla':/drone/src -v '/host/some/path':/container/mount/point`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotVolumeString := convertVolumesToString(tt.args.pipelineOS, tt.args.sourcedir, tt.args.stepVolumes, tt.args.pipeLineVolumeMap); gotVolumeString != tt.wantVolumeString {
				t.Errorf("convertVolumesToString() = #%v#, want #%v#", gotVolumeString, tt.wantVolumeString)
			}
		})
	}
}

func Test_convertStepNametoContainerString(t *testing.T) {
	tests := []struct {
		stepName          string
		wantContainerName string
	}{
		{
			stepName:          " spaces ",
			wantContainerName: `--name='spaces'`,
		},
		{
			stepName:          "weird characters &",
			wantContainerName: `--name='weirdcharacters'`,
		},
		{
			stepName:          "valid weird characters _-.",
			wantContainerName: `--name='validweirdcharacters_-.'`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.stepName, func(t *testing.T) {
			if gotContainerName := convertStepNametoContainerString(tt.stepName); gotContainerName != tt.wantContainerName {
				t.Errorf("convertStepNametoContainerString() = #%v#, want #%v#", gotContainerName, tt.wantContainerName)
			}
		})
	}
}

func Test_convertCommandsToEntryPointString(t *testing.T) {
	tests := []struct {
		name           string
		commands       []string
		pipelineOS     string
		wantEntryPoint string
	}{
		{
			name:           "no commands",
			commands:       []string{},
			pipelineOS:     "linux",
			wantEntryPoint: ``,
		},
		{
			name:           "single command",
			commands:       []string{"command"},
			pipelineOS:     "linux",
			wantEntryPoint: `/bin/bash -c " command;"`,
		},
		{
			name:           "multiple commands",
			commands:       []string{"command", "command2"},
			pipelineOS:     "linux",
			wantEntryPoint: `/bin/bash -c " command; command2;"`,
		},
		{
			name:           "multiple commands with params",
			commands:       []string{"command --a", "command2 --b"},
			pipelineOS:     "linux",
			wantEntryPoint: `/bin/bash -c " command --a; command2 --b;"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotEntryPoint := convertCommandsToEntryPointString(tt.pipelineOS, tt.commands); gotEntryPoint != tt.wantEntryPoint {
				t.Errorf("convertCommandsToEntryPointString() = '%v', want '%v'", gotEntryPoint, tt.wantEntryPoint)
			}
		})
	}
}
