// Copyright 2026 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"strconv"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestBuildIdentityVMLabels(t *testing.T) {
	tests := []struct {
		name        string
		setupParams *types.SetupInstanceParams
		timeout     int64
		env         string
		poolName    string
		source      types.InstanceSource
		wantKeys    map[string]string
		notWantKeys []string
	}{
		{
			name: "CI provision with full identity, mixed-case IDs lowercased",
			setupParams: &types.SetupInstanceParams{
				AccountID:           "Acc-AbC123",
				StageRuntimeID:      "StAgE-1",
				PipelineExecutionID: "PiPe-1_XYZ",
			},
			timeout:  3600,
			env:      "prod",
			poolName: "linux-amd64",
			source:   types.InstanceSourcePool,
			wantKeys: map[string]string{
				LabelCreatedBy:           identityCreatedBy,
				LabelAccountID:           "acc-abc123",
				LabelStageExecutionID:    "stage-1",
				LabelPipelineExecutionID: "pipe-1_xyz",
				LabelLongRunning:         "false",
				"pool_id":                "linux-amd64",
				"source":                 string(types.InstanceSourcePool),
				"harness_env":            "prod",
			},
		},
		{
			name: "long-running label flips on at >24h",
			setupParams: &types.SetupInstanceParams{
				StageRuntimeID: "stage-2",
			},
			timeout:  longRunningCutoff + 1,
			env:      "prod",
			poolName: "linux-amd64",
			source:   types.InstanceSourcePool,
			wantKeys: map[string]string{
				LabelLongRunning: "true",
			},
		},
		{
			name:        "warm-pool fill (no setupParams) omits identity keys",
			setupParams: nil,
			timeout:     0,
			env:         "prod",
			poolName:    "linux-amd64",
			source:      types.InstanceSourcePool,
			wantKeys: map[string]string{
				LabelCreatedBy:   identityCreatedBy,
				LabelLongRunning: "false",
				"pool_id":        "linux-amd64",
				"source":         string(types.InstanceSourcePool),
				"harness_env":    "prod",
			},
			notWantKeys: []string{LabelAccountID, LabelStageExecutionID, LabelPipelineExecutionID},
		},
		{
			name: "empty env is not written as a label",
			setupParams: &types.SetupInstanceParams{
				StageRuntimeID: "stage-3",
			},
			timeout:     3600,
			env:         "",
			poolName:    "linux-amd64",
			source:      types.InstanceSourcePool,
			notWantKeys: []string{"harness_env"},
		},
		{
			name: "empty individual identity fields omit their label",
			setupParams: &types.SetupInstanceParams{
				AccountID:           "",
				StageRuntimeID:      "stage-only",
				PipelineExecutionID: "",
			},
			timeout:     3600,
			env:         "prod",
			poolName:    "linux-amd64",
			source:      types.InstanceSourcePool,
			wantKeys:    map[string]string{LabelStageExecutionID: "stage-only"},
			notWantKeys: []string{LabelAccountID, LabelPipelineExecutionID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildIdentityVMLabels(tc.setupParams, tc.timeout, tc.env, tc.poolName, tc.source)
			for k, want := range tc.wantKeys {
				if got[k] != want {
					t.Errorf("label %q: got %q, want %q", k, got[k], want)
				}
			}
			for _, k := range tc.notWantKeys {
				if _, ok := got[k]; ok {
					t.Errorf("label %q should not be set, but got %q", k, got[k])
				}
			}
			if _, ok := got[LabelCreatedAt]; !ok {
				t.Errorf("missing %q label", LabelCreatedAt)
			}
			if v, ok := got[LabelCreatedAt]; ok {
				ts, parseErr := strconv.ParseInt(v, 10, 64)
				if parseErr != nil {
					t.Errorf("%q not a valid int64: %q", LabelCreatedAt, v)
				}
				if ts < time.Now().Unix()-5 || ts > time.Now().Unix()+5 {
					t.Errorf("%q timestamp far from now: %d", LabelCreatedAt, ts)
				}
			}
		})
	}
}

func TestBuildClaimIdentityLabels(t *testing.T) {
	tests := []struct {
		name        string
		setupParams *types.SetupInstanceParams
		timeout     int64
		wantKeys    map[string]string
		notWantKeys []string
	}{
		{
			name: "claim with full identity, mixed-case IDs lowercased",
			setupParams: &types.SetupInstanceParams{
				AccountID:           "Acc-AbC123",
				StageRuntimeID:      "StAgE-1",
				PipelineExecutionID: "PiPe-1_XYZ",
			},
			timeout: 3600,
			wantKeys: map[string]string{
				LabelAccountID:           "acc-abc123",
				LabelStageExecutionID:    "stage-1",
				LabelPipelineExecutionID: "pipe-1_xyz",
				LabelLongRunning:         "false",
			},
			notWantKeys: []string{"pool_id", "source", "harness_env", LabelCreatedBy},
		},
		{
			name: "long-running flips on at >24h",
			setupParams: &types.SetupInstanceParams{
				StageRuntimeID: "stage-2",
			},
			timeout: longRunningCutoff + 1,
			wantKeys: map[string]string{
				LabelLongRunning: "true",
			},
		},
		{
			name:        "nil setupParams returns only constant overlay keys",
			setupParams: nil,
			timeout:     0,
			wantKeys: map[string]string{
				LabelLongRunning: "false",
			},
			notWantKeys: []string{LabelAccountID, LabelStageExecutionID, LabelPipelineExecutionID},
		},
		{
			name: "missing stage / pipeline ids omit those keys",
			setupParams: &types.SetupInstanceParams{
				AccountID: "acc-1",
			},
			timeout:     3600,
			wantKeys:    map[string]string{LabelAccountID: "acc-1"},
			notWantKeys: []string{LabelStageExecutionID, LabelPipelineExecutionID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildClaimIdentityLabels(tc.setupParams, tc.timeout)
			for k, want := range tc.wantKeys {
				if got[k] != want {
					t.Errorf("label %q: got %q, want %q", k, got[k], want)
				}
			}
			for _, k := range tc.notWantKeys {
				if _, ok := got[k]; ok {
					t.Errorf("label %q should not be set, but got %q", k, got[k])
				}
			}
			if _, ok := got[LabelCreatedAt]; !ok {
				t.Errorf("missing %q label", LabelCreatedAt)
			}
			if v, ok := got[LabelCreatedAt]; ok {
				ts, parseErr := strconv.ParseInt(v, 10, 64)
				if parseErr != nil {
					t.Errorf("%q not a valid int64: %q", LabelCreatedAt, v)
				}
				if ts < time.Now().Unix()-5 || ts > time.Now().Unix()+5 {
					t.Errorf("%q timestamp far from now: %d", LabelCreatedAt, ts)
				}
			}
		})
	}
}

func TestBuildClaimIdentityMetadata(t *testing.T) {
	tests := []struct {
		name        string
		setupParams *types.SetupInstanceParams
		want        map[string]string
	}{
		{
			name: "raw case is preserved for all three ids",
			setupParams: &types.SetupInstanceParams{
				AccountID:           "Acc-AbC123",
				StageRuntimeID:      "StAgE-1",
				PipelineExecutionID: "PiPe-1_XYZ",
			},
			want: map[string]string{
				MetadataAccountID:           "Acc-AbC123",
				MetadataStageExecutionID:    "StAgE-1",
				MetadataPipelineExecutionID: "PiPe-1_XYZ",
			},
		},
		{
			name:        "nil setupParams returns nil",
			setupParams: nil,
			want:        nil,
		},
		{
			name:        "empty setupParams returns nil (no keys)",
			setupParams: &types.SetupInstanceParams{},
			want:        nil,
		},
		{
			name: "only the populated ids are included",
			setupParams: &types.SetupInstanceParams{
				StageRuntimeID: "stage-only",
			},
			want: map[string]string{
				MetadataStageExecutionID: "stage-only",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildClaimIdentityMetadata(tc.setupParams)
			if tc.want == nil {
				if got != nil {
					t.Errorf("want nil, got %v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Errorf("len mismatch: got %d (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("metadata %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
