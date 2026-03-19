package drivers

import "testing"

func TestShortCallerPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "unix path keeps last three components",
			path:     "/home/runner/work/drone-runner-aws/app/drivers/instance_ops.go",
			expected: "app/drivers/instance_ops.go",
		},
		{
			name:     "windows path keeps last three components",
			path:     `C:\agent\_work\1\s\app\drivers\instance_ops.go`,
			expected: "app/drivers/instance_ops.go",
		},
		{
			name:     "short path remains unchanged",
			path:     "instance_ops.go",
			expected: "instance_ops.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shortCallerPath(tt.path); got != tt.expected {
				t.Fatalf("shortCallerPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}
