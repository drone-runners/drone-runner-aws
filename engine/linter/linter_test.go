// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package linter

import (
	"path"
	"testing"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/drivers/amazon"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/types"
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

func Test_checkPools(t *testing.T) {
	type args struct {
		pipeline    *resource.Pipeline
		poolManager *drivers.Manager
		autoPool    bool
	}

	poolInstance := DummyPool("testpoolname", "runnername")
	poolManagerEmpty := &drivers.Manager{}
	poolManagerWithOne := &drivers.Manager{}
	_ = poolManagerWithOne.Add(poolInstance)

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "pool match !",
			args: args{
				pipeline: &resource.Pipeline{
					Name: "pipeline with pool to use",
					Pool: resource.Pool{
						Use: "testpoolname",
					},
				},
				poolManager: poolManagerWithOne,
				autoPool:    false,
			},
			wantErr: false,
		},
		{
			name: "no pools",
			args: args{
				pipeline:    &resource.Pipeline{},
				poolManager: poolManagerEmpty,
				autoPool:    false,
			},
			wantErr: true,
		},
		{
			name: "pool name is empty",
			args: args{
				pipeline: &resource.Pipeline{
					Name: "pipeline with no pool to use",
					Pool: resource.Pool{
						Use: "",
					},
				},
				poolManager: poolManagerWithOne,
				autoPool:    false,
			},
			wantErr: true,
		},
		{
			name: "pool doesnt exist in map",
			args: args{
				pipeline: &resource.Pipeline{
					Name: "pipeline with no pool to use",
					Pool: resource.Pool{
						Use: "no one here",
					},
				},
				poolManager: poolManagerWithOne,
				autoPool:    false,
			},
			wantErr: true,
		},
		{
			name: "auto pool is true and platform match",
			args: args{
				pipeline: &resource.Pipeline{
					Name: "pipeline with no pool to use",
				},
				poolManager: poolManagerWithOne,
				autoPool:    true,
			},
			wantErr: false,
		},
		{
			name: "auto pool is true and NO platform match",
			args: args{
				pipeline: &resource.Pipeline{
					Name: "pipeline with no pool to use",
					Platform: types.Platform{
						OS:   "windows",
						Arch: "amd64",
					},
				},
				poolManager: poolManagerWithOne,
				autoPool:    true,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkPools(tt.args.pipeline, tt.args.poolManager, tt.args.autoPool); (err != nil) != tt.wantErr {
				t.Errorf("checkPools() error = %v, wantErr %v", err, tt.wantErr)
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
	pool.Platform = types.Platform{
		OS:   "linux",
		Arch: "amd64",
	}
	if err != nil {
		return pool
	}
	return pool
}
