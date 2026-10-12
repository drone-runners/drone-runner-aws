package types

import "testing"

func TestParseTier(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", TierPaid, false},
		{"paid", TierPaid, false},
		{" Paid ", TierPaid, false},
		{"free", TierFree, false},
		{"FREE", TierFree, false},
		{"premium", "", true},
	}
	for _, tc := range cases {
		got, err := ParseTier(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseTier(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseTier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseSchedulerAlgorithm(t *testing.T) {
	cases := []struct {
		in      string
		want    SchedulerAlgorithm
		wantErr bool
	}{
		{"", SchedulerAlgorithmPool, false},
		{"pool", SchedulerAlgorithmPool, false},
		{"Capability", SchedulerAlgorithmCapability, false},
		{"magic", "", true},
	}
	for _, tc := range cases {
		got, err := ParseSchedulerAlgorithm(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseSchedulerAlgorithm(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSchedulerAlgorithm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCapabilityKeyString(t *testing.T) {
	k := CapabilityKey{Tier: TierPaid, OS: "linux", Arch: "amd64", VariantID: "variant_medium", TenantID: "default"}
	if got, want := k.String(), "paid/linux/amd64/variant_medium/default"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestCapabilityScaleJobName(t *testing.T) {
	k := CapabilityKey{Tier: TierFree, OS: "linux", Arch: "arm64", VariantID: "default", TenantID: "acct-1"}
	name := CapabilityScaleJobName(&k)
	if name != "cap:free/linux/arm64/default/acct-1" {
		t.Errorf("name = %q", name)
	}
	if got, ok := ParseCapabilityScaleJobName(name); !ok || got != k.String() {
		t.Errorf("parse(%q) = %q, %v", name, got, ok)
	}
	if _, ok := ParseCapabilityScaleJobName("linux-amd64"); ok {
		t.Error("pool names must not parse as capability jobs")
	}
}
