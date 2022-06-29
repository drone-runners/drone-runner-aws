// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package linter

import (
	"path"
	"testing"

	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/drivers/amazon"
	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/manifest"
)

func TestLint(t *testing.T) {
	tests := []struct {
		path    string
		trusted bool
		invalid bool
		message string
	}{
		{
			path:    "testdata/simple.yml",
			trusted: false,
			invalid: false,
		},
	}
	for _, test := range tests {
		name := path.Base(test.path)
		if test.trusted {
			name += "/trusted"
		}
		t.Run(name, func(t *testing.T) {
			resources, err := manifest.ParseFile(test.path)
			if err != nil {
				t.Logf("yaml: %s", test.path)
				t.Logf("trusted: %v", test.trusted)
				t.Error(err)
				return
			}

			pool := DummyPool("cats", "runner")

			poolManager := &drivers.Manager{}
			err = poolManager.Add(pool)
			if err != nil {
				t.Error(err)
				return
			}

			lint := New(false)
			lint.PoolManager = poolManager

			opts := &drone.Repo{Trusted: test.trusted}
			err = lint.Lint(resources.Resources[0].(*resource.Pipeline), opts)
			if err == nil && test.invalid == true {
				t.Logf("yaml: %s", test.path)
				t.Logf("trusted: %v", test.trusted)
				t.Errorf("Expect lint error")
				return
			}

			if err != nil && test.invalid == false {
				t.Logf("yaml: %s", test.path)
				t.Logf("trusted: %v", test.trusted)
				t.Errorf("Expect lint error is nil, got %s", err)
				return
			}

			if err == nil {
				return
			}

			if got, want := err.Error(), test.message; got != want {
				t.Logf("yaml: %s", test.path)
				t.Logf("trusted: %v", test.trusted)
				t.Errorf("Want message %q, got %q", want, got)
				return
			}
		})
	}
}

func DummyPool(name, runnerName string) drivers.Pool {
	var pool drivers.Pool
	pool.Name = name
	pool.RunnerName = runnerName
	var driver, err = amazon.New()
	pool.Driver = driver
	if err != nil {
		return pool
	}
	return pool
}
