package types

import (
	"encoding/json"
	"testing"
)

func TestInstanceSourceConstants(t *testing.T) {
	tests := []struct {
		source InstanceSource
		want   string
	}{
		{InstanceSourceUnknown, "unknown"},
		{InstanceSourcePool, "pool"},
		{InstanceSourcePredictor, "predictor"},
		{InstanceSourceOnDemand, "ondemand"},
	}
	for _, tt := range tests {
		if string(tt.source) != tt.want {
			t.Errorf("expected %q, got %q", tt.want, string(tt.source))
		}
	}
}

func TestSetupInstanceParams_SourceJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		source InstanceSource
	}{
		{"pool", InstanceSourcePool},
		{"predictor", InstanceSourcePredictor},
		{"ondemand", InstanceSourceOnDemand},
		{"unknown", InstanceSourceUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := SetupInstanceParams{
				VariantID: "variant-1",
				ImageName: "ubuntu-2204",
				Source:    tt.source,
			}

			data, err := json.Marshal(params)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded SetupInstanceParams
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Source != tt.source {
				t.Errorf("Source: expected %q, got %q", tt.source, decoded.Source)
			}
			if decoded.VariantID != "variant-1" {
				t.Errorf("VariantID: expected %q, got %q", "variant-1", decoded.VariantID)
			}
		})
	}
}

func TestSetupInstanceParams_EmptySourceOmittedInJSON(t *testing.T) {
	params := SetupInstanceParams{VariantID: "v1"}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Source has omitempty, so empty source should not appear in JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	if _, exists := raw["source"]; exists {
		t.Error("expected empty source to be omitted from JSON, but it was present")
	}
}

func TestSetupInstanceParams_IdentityFieldsJSONRoundTrip(t *testing.T) {
	in := SetupInstanceParams{
		AccountID:           "acc-123",
		StageRuntimeID:      "stage-456",
		PipelineExecutionID: "pe-789",
		LongRunning:         true,
		CreatedAt:           1700000000,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out SetupInstanceParams
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.AccountID != in.AccountID {
		t.Errorf("AccountID: got %q want %q", out.AccountID, in.AccountID)
	}
	if out.StageRuntimeID != in.StageRuntimeID {
		t.Errorf("StageRuntimeID: got %q want %q", out.StageRuntimeID, in.StageRuntimeID)
	}
	if out.PipelineExecutionID != in.PipelineExecutionID {
		t.Errorf("PipelineExecutionID: got %q want %q", out.PipelineExecutionID, in.PipelineExecutionID)
	}
	if out.LongRunning != in.LongRunning {
		t.Errorf("LongRunning: got %v want %v", out.LongRunning, in.LongRunning)
	}
	if out.CreatedAt != in.CreatedAt {
		t.Errorf("CreatedAt: got %d want %d", out.CreatedAt, in.CreatedAt)
	}
}
