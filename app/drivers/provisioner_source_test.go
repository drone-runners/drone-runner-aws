package drivers

import (
	"context"
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestSetupInstanceParams_SourcePassthrough(t *testing.T) {
	tests := []struct {
		name             string
		params           *types.SetupInstanceParams
		expected         types.InstanceSource
		disableFallbacks bool
	}{
		{
			name:             "pool source",
			params:           &types.SetupInstanceParams{Source: types.InstanceSourcePool},
			expected:         types.InstanceSourcePool,
			disableFallbacks: true,
		},
		{
			name:             "predictor source",
			params:           &types.SetupInstanceParams{Source: types.InstanceSourcePredictor},
			expected:         types.InstanceSourcePredictor,
			disableFallbacks: true,
		},
		{
			name:             "ondemand source",
			params:           &types.SetupInstanceParams{Source: types.InstanceSourceOnDemand},
			expected:         types.InstanceSourceOnDemand,
			disableFallbacks: false,
		},
		{
			name:             "empty source defaults to pool",
			params:           &types.SetupInstanceParams{},
			expected:         types.InstanceSourcePool,
			disableFallbacks: true,
		},
		{
			name:             "nil params defaults to pool",
			params:           nil,
			expected:         types.InstanceSourcePool,
			disableFallbacks: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveInstanceSource(tt.params)
			if got != tt.expected {
				t.Errorf("resolveInstanceSource() = %q, want %q", got, tt.expected)
			}
			if disabled := disableMachineTypeFallbacks(got); disabled != tt.disableFallbacks {
				t.Errorf("disableMachineTypeFallbacks(%q) = %t, want %t", got, disabled, tt.disableFallbacks)
			}
		})
	}
}

func TestSetupInstanceDisablesFallbacksForWarmCapacity(t *testing.T) {
	fallbacks := []types.MachineTypeFallback{{
		MachineType: "n2-standard-8",
		DiskType:    "pd-balanced",
	}}
	tests := []struct {
		name             string
		source           types.InstanceSource
		disableFallbacks bool
	}{
		{name: "pool", source: types.InstanceSourcePool, disableFallbacks: true},
		{name: "predictor", source: types.InstanceSourcePredictor, disableFallbacks: true},
		{name: "on demand", source: types.InstanceSourceOnDemand, disableFallbacks: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *types.InstanceCreateOpts
			driver := &flexibleMockDriver{
				CreateFunc: func(_ context.Context, opts *types.InstanceCreateOpts) (*types.Instance, error) {
					got = opts
					return &types.Instance{ID: "instance", Pool: "pool1", Zone: "us-central1-a"}, nil
				},
			}
			store := &mockInstanceStore{
				CreateFunc: func(_ context.Context, _ *types.Instance) error { return nil },
			}
			manager, pool := newSetupInstanceTestManager(store, driver, nil)
			params := &types.SetupInstanceParams{
				MachineType:          "c4d-standard-8",
				MachineTypeFallbacks: fallbacks,
				Source:               tt.source,
			}

			_, _, err := manager.setupInstance(
				context.Background(), pool, "tls", "owner", params, nil, true, nil, nil, 60, nil, nil, false,
			)
			if err != nil {
				t.Fatalf("setupInstance: %v", err)
			}
			if got == nil {
				t.Fatal("driver did not receive create options")
			}
			if got.DisableMachineTypeFallbacks != tt.disableFallbacks {
				t.Errorf("DisableMachineTypeFallbacks=%t, want %t", got.DisableMachineTypeFallbacks, tt.disableFallbacks)
			}
			if got.MachineType != params.MachineType {
				t.Errorf("MachineType=%q, want %q", got.MachineType, params.MachineType)
			}
			if len(got.MachineTypeFallbacks) != 1 || got.MachineTypeFallbacks[0] != fallbacks[0] {
				t.Errorf("MachineTypeFallbacks=%+v, want %+v", got.MachineTypeFallbacks, fallbacks)
			}
		})
	}
}
