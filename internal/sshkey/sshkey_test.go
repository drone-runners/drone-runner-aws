// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package sshkey

import "testing"

func TestGenerate(t *testing.T) {
	_, _, err := GeneratePair()
	if err != nil {
		t.Error(err)
	}
}
