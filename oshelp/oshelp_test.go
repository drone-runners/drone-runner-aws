// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package oshelp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/shell/bash"
	"github.com/drone/runner-go/shell/powershell"
)

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
		if got, want := JoinPaths(test.os, test.a...), test.b; got != want {
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
		if got, want := GetExt(test.os, test.a), test.b; got != want {
			t.Errorf("Want %s, got %s", want, got)
		}
	}
}

func Test_getCommand(t *testing.T) {
	cmd, args := GetCommand("linux", "clone.sh")
	if got, want := cmd, "/bin/sh"; got != want {
		t.Errorf("Want command %s, got %s", want, got)
	}
	if !reflect.DeepEqual(args, []string{"-e", "clone.sh"}) {
		t.Errorf("Unexpected args %v", args)
	}

	cmd, args = GetCommand("windows", "clone.ps1")
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
		if got, want := GetNetrc(test.os), test.name; got != want {
			t.Errorf("Want %s on %s, got %s", want, test.os, got)
		}
	}
}

func Test_getScript(t *testing.T) {
	commands := []string{"go build"}

	a := GenScript("windows", commands)
	b := powershell.Script(commands)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Generated windows linux script")
	}

	a = GenScript("linux", commands)
	b = bash.Script(commands)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Generated invalid linux script")
	}
}

func Test_convertEnvMapToString(t *testing.T) {
	tests := []struct {
		name          string
		args          map[string]string
		wantEnvString []string
	}{
		{
			name:          "one var",
			args:          map[string]string{"DRONE_BRANCH": "main"},
			wantEnvString: []string{` --env DRONE_BRANCH='main'`},
		},
		{
			name:          "empty var",
			args:          map[string]string{"DRONE_BRANCH": ""},
			wantEnvString: []string{``},
		},
		{
			name:          "multiple vars",
			args:          map[string]string{"DRONE_BRANCH": "main", "DRONE_BUILD": "5"},
			wantEnvString: []string{` --env DRONE_BRANCH='main'`, ` --env DRONE_BUILD='5'`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnvString := convertEnvMapToString(tt.args)
			for _, want := range tt.wantEnvString {
				if !strings.Contains(gotEnvString, want) {
					t.Errorf("convertEnvMapToString() = #%s# contains #%s#", gotEnvString, want)
				}
			}
		})
	}
}

func Test_convertSettingsToString(t *testing.T) {
	tests := []struct {
		name          string
		args          map[string]*manifest.Parameter
		wantEnvString []string
	}{
		{
			name:          "one value",
			args:          map[string]*manifest.Parameter{"setting_a": {Value: 7}},
			wantEnvString: []string{` --env PLUGIN_SETTING_A='7'`},
		},
		{
			name:          "one secret",
			args:          map[string]*manifest.Parameter{"setting_b": {Secret: "bla"}},
			wantEnvString: []string{` --env PLUGIN_SETTING_B='bla'`},
		},
		{
			name:          "one secret, one value",
			args:          map[string]*manifest.Parameter{"setting_a": {Value: 7}, "setting_b": {Secret: "bla"}},
			wantEnvString: []string{` --env PLUGIN_SETTING_A='7'`, ` --env PLUGIN_SETTING_B='bla'`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSettingsString := convertSettingsToString(tt.args)
			for _, want := range tt.wantEnvString {
				if !strings.Contains(gotSettingsString, want) {
					t.Errorf("convertSettingsToString() = #%s# contains #%s#", gotSettingsString, want)
				}
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
			if gotContainerName := convertStepNameToContainerString(tt.stepName); gotContainerName != tt.wantContainerName {
				t.Errorf("convertStepNameToContainerString() = #%v#, want #%v#", gotContainerName, tt.wantContainerName)
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
