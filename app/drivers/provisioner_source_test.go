package drivers

import (
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestSetupInstanceParams_SourcePassthrough(t *testing.T) {
	tests := []struct {
		name     string
		params   *types.SetupInstanceParams
		expected types.InstanceSource
	}{
		{
			name:     "pool source",
			params:   &types.SetupInstanceParams{Source: types.InstanceSourcePool},
			expected: types.InstanceSourcePool,
		},
		{
			name:     "predictor source",
			params:   &types.SetupInstanceParams{Source: types.InstanceSourcePredictor},
			expected: types.InstanceSourcePredictor,
		},
		{
			name:     "ondemand source",
			params:   &types.SetupInstanceParams{Source: types.InstanceSourceOnDemand},
			expected: types.InstanceSourceOnDemand,
		},
		{
			name:     "empty source defaults to pool",
			params:   &types.SetupInstanceParams{},
			expected: types.InstanceSourcePool,
		},
		{
			name:     "nil params defaults to pool",
			params:   nil,
			expected: types.InstanceSourcePool,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveInstanceSource(tt.params)
			if got != tt.expected {
				t.Errorf("resolveInstanceSource() = %q, want %q", got, tt.expected)
			}
		})
	}
}
