// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package powershell provides functions for converting shell
// commands to powershell scripts.
package powershell

import (
	"bytes"
	"fmt"
	"strings"
)

// Suffix provides the shell script suffix.
const Suffix = ".ps1"

// Command returns the Powershell command and arguments.
func Command() (string, []string) {
	return "powershell", []string{
		"-noprofile",
		"-noninteractive",
		"-command",
	}
}

func Script(commands []string) string {
	return script(commands, true)
}

func SilentScript(commands []string) string {
	return script(commands, false)
}

// Script converts a slice of individual shell commands to
// a powershell script.
func script(commands []string, trace bool) string {
	buf := new(bytes.Buffer)
	fmt.Fprintln(buf)
	fmt.Fprintf(buf, optionScript)
	fmt.Fprintln(buf)
	for _, command := range commands {
		escaped := fmt.Sprintf("%q", "+ "+command)
		escaped = strings.Replace(escaped, "$", "`$", -1)
		var stringToWrite string
		if trace {
			stringToWrite = fmt.Sprintf(
				traceScript,
				escaped,
				command,
			)
		} else {
			stringToWrite = "\n" + command + "\nif ($LastExitCode -gt 0) { exit $LastExitCode }\n"
		}
		buf.WriteString(stringToWrite)
	}
	return buf.String()
}

// optionScript is a helper script this is added to the build
// to set shell options, in this case, to exit on error.
const optionScript = `$erroractionpreference = "stop"`

// traceScript is a helper script that is added to the build script to trace a command.
const traceScript = `
echo %s
%s
if ($LastExitCode -gt 0) { exit $LastExitCode }
`
