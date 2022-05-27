// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package oshelp

import (
	"strings"

	"github.com/drone/runner-go/shell/bash"
	"github.com/drone/runner-go/shell/powershell"
)

const OSWindows = "windows"
const OSLinux = "linux"
const OSMac = "darwin"
const ArchAMD64 = "amd64"
const ArchARM64 = "arm64"

const Ubuntu = "ubuntu"
const AmazonLinux = "amazon-linux"

// JoinPaths helper function joins the file paths.
func JoinPaths(os string, paths ...string) string {
	switch os {
	case OSWindows:
		return strings.Join(paths, "\\")
	default:
		return strings.Join(paths, "/")
	}
}

// GetExt helper function returns the shell extension based on the
// target platform.
func GetExt(os, file string) (s string) {
	switch os {
	case OSWindows:
		return file + ".ps1"
	default:
		return file
	}
}

// GetNetrc helper function returns the netrc file name based on the target platform.
func GetNetrc(os string) string {
	switch os {
	case OSWindows:
		return "_netrc"
	default:
		return ".netrc"
	}
}

// GenScript helper function generates and returns a shell script to
// execute the provided shell commands. The shell scripting
// language (bash vs powershell) is determined by the operating
// system.
func GenScript(os string, commands []string) string {
	switch os {
	case OSWindows:
		return powershell.Script(commands)
	case OSMac:
		macScript := bash.Script(commands)
		macScript = "PATH=$PATH:/usr/local/bin" + macScript
		return macScript
	default:
		return bash.Script(commands)
	}
}
