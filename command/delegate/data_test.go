package delegate

import (
	"bytes"
	"os"
	"testing"
)

func TestGetSetupRequestHappyPath(t *testing.T) {
	raw, _ := os.ReadFile("test_data/setup_happy_payload.json")
	got, err := GetSetupRequest(bytes.NewReader(raw))
	if err != nil {
		t.Errorf("GetSetupRequest() error should be nil, got %v", err)
		return
	}

	if got.DataDump.Class != "io.harness.delegate.beans.ci.CIK8BuildTaskParams" {
		t.Errorf("Address should be equal '%s'", got.DataDump.Class)
	}
}
