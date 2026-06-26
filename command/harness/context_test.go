package harness

import "testing"

func TestGetPipelineExecutionID(t *testing.T) {
	tests := []struct {
		name string
		ctx  *Context
		tags map[string]string
		want string
	}{
		{
			name: "context value wins",
			ctx:  &Context{PipelineExecutionID: "pe-from-ctx"},
			tags: map[string]string{"pipelineExecutionID": "pe-from-tags"},
			want: "pe-from-ctx",
		},
		{
			name: "falls back to tags when context empty",
			ctx:  &Context{},
			tags: map[string]string{"pipelineExecutionID": "pe-from-tags"},
			want: "pe-from-tags",
		},
		{
			name: "empty when neither set",
			ctx:  &Context{},
			tags: map[string]string{},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getPipelineExecutionID(tc.ctx, tc.tags)
			if got != tc.want {
				t.Fatalf("getPipelineExecutionID = %q, want %q", got, tc.want)
			}
		})
	}
}
