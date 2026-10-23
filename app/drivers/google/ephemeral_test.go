package google

import (
	"reflect"
	"testing"
)

func TestInstanceNetworkTags(t *testing.T) {
	tests := []struct {
		name      string
		ephemeral bool
		tags      []string
		want      []string
	}{
		{
			name:      "non-ephemeral keeps allow-docker and appends the name",
			ephemeral: false,
			tags:      []string{"allow-docker", "custom"},
			want:      []string{"allow-docker", "custom", "vm-1"},
		},
		{
			name:      "ephemeral drops allow-docker only",
			ephemeral: true,
			tags:      []string{"allow-docker", "custom"},
			want:      []string{"custom", "vm-1"},
		},
		{
			name:      "ephemeral with default tags leaves just the name",
			ephemeral: true,
			tags:      defaultTags,
			want:      []string{"vm-1"},
		},
		{
			name:      "ephemeral with no tags leaves just the name",
			ephemeral: true,
			tags:      nil,
			want:      []string{"vm-1"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := instanceNetworkTags(tc.ephemeral, tc.tags, "vm-1")
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("instanceNetworkTags() = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("input slice is not mutated", func(t *testing.T) {
		in := []string{"allow-docker", "custom"}
		_ = instanceNetworkTags(true, in, "vm-1")
		if want := []string{"allow-docker", "custom"}; !reflect.DeepEqual(in, want) {
			t.Errorf("input mutated: %v", in)
		}
	})
}
