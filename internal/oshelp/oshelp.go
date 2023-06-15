// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package oshelp

import (
	"fmt"
	"strings"

	"github.com/dchest/uniuri"

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

// Path to store the lite engine logs inside the virtual machine
func GetLiteEngineLogsPath(os string) string {
	switch os {
	case OSMac:
		return "/Users/anka/lite-engine.log"
	case OSWindows:
		return `C:\Program Files\lite-engine\log.out`
	default:
		return "/var/log/lite-engine.log"
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
func GenScript(os, arch string, commands []string) string {
	switch os {
	case OSWindows:
		return powershell.Script(commands)
	case OSMac:
		macScript := bash.Script(commands)
		macScript = "PATH=$PATH:/usr/local/bin" + macScript
		return macScript
	default:
		return returnTmateScript(arch) + bash.Script(commands)
	}
}

func returnTmateScript(arch string) (script string) {
	script = fmt.Sprintf(
		`
remote_debug() {
	if [ "$?" -ne "0" ]; then
		wget https://github.com/tmate-io/tmate/releases/download/2.4.0/tmate-2.4.0-static-linux-%s.tar.xz
		mkdir -p /usr/drone/bin/
		tar -xf tmate-2.4.0-static-linux-%s.tar.xz
		mv tmate-2.4.0-static-linux-%s/tmate /usr/drone/bin/
		chmod +x /usr/drone/bin/tmate
		/usr/drone/bin/tmate -F
	fi
}

if [ ! -z "${DRONE_TMATE_HOST}" ]; then
	echo "set -g tmate-server-host $DRONE_TMATE_HOST" >> $HOME/.tmate.conf
	echo "set -g tmate-server-port $DRONE_TMATE_PORT" >> $HOME/.tmate.conf
	echo "set -g tmate-server-rsa-fingerprint $DRONE_TMATE_FINGERPRINT_RSA" >> $HOME/.tmate.conf
	echo "set -g tmate-server-ed25519-fingerprint $DRONE_TMATE_FINGERPRINT_ED25519" >> $HOME/.tmate.conf
fi

if [ "${DRONE_BUILD_DEBUG}" = "true" ]; then
	trap remote_debug EXIT
fi
`, arch, arch, arch)
	return script
}

func GetEntrypoint(pipelineOS string) []string {
	if pipelineOS == OSWindows {
		return []string{"powershell"}
	}

	return []string{"sh", "-c"}
}

// Random generator function
var Random = func() string {
	return "drone-" + uniuri.NewLen(20) //nolint:gomnd
}
