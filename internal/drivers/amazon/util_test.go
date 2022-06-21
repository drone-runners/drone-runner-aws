// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package amazon

import (
	"testing"

	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
)

func Test_tempdir(t *testing.T) {
	tests := []struct {
		os   string
		path string
	}{
		{os: oshelp.OSWindows, path: "C:\\Windows\\Temp\\aws"},
		{os: oshelp.OSLinux, path: "/tmp/aws"},
	}

	for _, test := range tests {
		if got, want := tempdir(test.os), test.path; got != want {
			t.Errorf("Want tempdir %s, got %s", want, got)
		}
	}
}
