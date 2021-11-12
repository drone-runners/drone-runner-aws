// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package cloudaws

import (
	"testing"
)

func Test_tempdir(t *testing.T) {
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
