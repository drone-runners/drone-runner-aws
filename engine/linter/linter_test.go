// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package linter

import (
	"path"
	"testing"

	"github.com/drone-runners/drone-runner-aws/engine"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
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

			pools := make(map[string]engine.Pool)
			pools["cats"] = engine.Pool{
				Name: "cats"}

			lint := New()
			lint.Pools = pools
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
		pipeline *resource.Pipeline
		pools    map[string]engine.Pool
	}
	poolInstance := engine.Pool{
		Name: "test",
	}
	poolWithOne := map[string]engine.Pool{
		"test": poolInstance,
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "no pools",
			args: args{
				pipeline: &resource.Pipeline{},
				pools:    make(map[string]engine.Pool),
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
				pools: poolWithOne,
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
				pools: poolWithOne,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkPools(tt.args.pipeline, tt.args.pools); (err != nil) != tt.wantErr {
				t.Errorf("checkPools() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
