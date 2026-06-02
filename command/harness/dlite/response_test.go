package dlite

import (
	"encoding/json"
	"strings"
	"testing"

	lespec "github.com/harness/lite-engine/engine/spec"
)

// TestVMTaskExecutionResponse_OSStatsOmittedWhenNil ensures the os_stats
// field is absent from the dlite response when no snapshot is attached.
// Setup, exec and capacity responses must not bloat the payload.
func TestVMTaskExecutionResponse_OSStatsOmittedWhenNil(t *testing.T) {
	resp := VMTaskExecutionResponse{
		CommandExecutionStatus: Success,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "os_stats") {
		t.Fatalf("expected os_stats to be omitted when nil; got %s", b)
	}
}

// TestVMTaskExecutionResponse_OSStatsRoundTrip locks in the wire contract
// between drone-runner-aws (dlite cleanup response) and CI Manager
// (VmTaskExecutionResponse.OSStats). All percentile and disk fields the
// resolver depends on must serialise under their canonical JSON tags.
func TestVMTaskExecutionResponse_OSStatsRoundTrip(t *testing.T) {
	resp := VMTaskExecutionResponse{
		CommandExecutionStatus: Success,
		OSStats: &lespec.OSStats{
			TotalMemMB:       8192,
			CPUCores:         4,
			AvgMemUsagePct:   42.5,
			AvgCPUUsagePct:   30.1,
			MaxMemUsagePct:   88.2,
			MaxCPUUsagePct:   95.7,
			P95MemUsagePct:   85,
			P95CPUUsagePct:   90,
			TotalDiskMB:      102400,
			AvgDiskUsagePct:  60,
			PeakDiskUsagePct: 75,
			P95DiskUsagePct:  72,
		},
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded VMTaskExecutionResponse
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.OSStats == nil {
		t.Fatal("expected OSStats to round-trip, got nil")
	}
	if decoded.OSStats.P95MemUsagePct != 85 || decoded.OSStats.P95CPUUsagePct != 90 ||
		decoded.OSStats.P95DiskUsagePct != 72 || decoded.OSStats.PeakDiskUsagePct != 75 {
		t.Fatalf("os_stats fields did not round-trip: got %+v", decoded.OSStats)
	}

	for _, key := range []string{
		"\"os_stats\"",
		"\"p95_mem_usage_pct\"",
		"\"p95_cpu_usage_pct\"",
		"\"p95_disk_usage_pct\"",
		"\"peak_disk_usage_pct\"",
		"\"avg_disk_usage_pct\"",
		"\"total_disk_mb\"",
	} {
		if !strings.Contains(string(b), key) {
			t.Errorf("expected JSON to contain %s, got: %s", key, b)
		}
	}
}
