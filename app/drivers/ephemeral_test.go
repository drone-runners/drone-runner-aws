package drivers

import (
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

func ephemeralPool(spec *config.Google) *Pool {
	return &Pool{
		Name:     "pool-a",
		Platform: types.Platform{OS: "linux", Arch: "amd64"},
		Driver:   &googleMockDriver{},
		Spec:     spec,
	}
}

func ephemeralInput(pool *Pool) EphemeralCreateInput {
	return EphemeralCreateInput{
		Pool:                pool,
		RunnerName:          "runner-1",
		Env:                 "qa",
		AccountID:           "AcctOne",
		StageRuntimeID:      "Stage1",
		PipelineExecutionID: "Exec1",
		DelegatePath:        "https://dl.example.com/delegates/3.9.0",
		Token:               "tok",
		ManagerEndpoint:     "https://app.harness.io",
		TransactionID:       "txn1",
		DelegateName:        "vm-delegate",
		Tags:                "linux-amd64",
		Zones:               []string{"us-central1-a"},
		MachineType:         "e2-medium",
		ImageName:           "projects/p/global/images/img",
		Timeout:             600,
	}
}

func TestPrepareEphemeralCreateOptsErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   func() EphemeralCreateInput
		wantErr string
	}{
		{
			name:    "nil pool",
			input:   func() EphemeralCreateInput { in := ephemeralInput(nil); return in },
			wantErr: "pool is required",
		},
		{
			name: "missing delegate path",
			input: func() EphemeralCreateInput {
				in := ephemeralInput(ephemeralPool(&config.Google{}))
				in.DelegatePath = ""
				return in
			},
			wantErr: "delegate download path is required",
		},
		{
			name: "unsupported driver",
			input: func() EphemeralCreateInput {
				pool := ephemeralPool(&config.Google{})
				pool.Driver = &mockDriver{}
				return ephemeralInput(pool)
			},
			wantErr: "drivers only",
		},
		{
			name: "unsupported platform",
			input: func() EphemeralCreateInput {
				in := ephemeralInput(ephemeralPool(&config.Google{}))
				in.Platform = types.Platform{OS: "freebsd", Arch: "amd64"}
				return in
			},
			wantErr: "windows only",
		},
		{
			name: "egress pool without ca certificate",
			input: func() EphemeralCreateInput {
				return ephemeralInput(ephemeralPool(&config.Google{EgressControl: true}))
			},
			wantErr: "egress CA certificate is required",
		},
		{
			name: "pool spec is not google",
			input: func() EphemeralCreateInput {
				pool := ephemeralPool(nil)
				pool.Spec = &config.Amazon{}
				return ephemeralInput(pool)
			},
			wantErr: "google pool spec",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input()
			_, err := PrepareEphemeralCreateOpts(&in)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("PrepareEphemeralCreateOpts error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestPrepareEphemeralCreateOpts(t *testing.T) {
	in := ephemeralInput(ephemeralPool(&config.Google{}))
	opts, err := PrepareEphemeralCreateOpts(&in)
	if err != nil {
		t.Fatalf("PrepareEphemeralCreateOpts: %v", err)
	}
	if !opts.EphemeralDelegate {
		t.Error("EphemeralDelegate must be true")
	}
	assertPreparedIdentity(t, opts)
	assertPreparedLabels(t, opts)
}

func assertPreparedIdentity(t *testing.T, opts *types.InstanceCreateOpts) {
	t.Helper()
	if opts.DelegatePath != "https://dl.example.com/delegates/3.9.0" || opts.DelegateToken != "tok" ||
		opts.DelegateManagerEndpoint != "https://app.harness.io" || opts.DelegateTransactionID != "txn1" ||
		opts.DelegateName != "vm-delegate" || opts.DelegateTags != "linux-amd64" {
		t.Errorf("delegate fields not carried over: %+v", opts)
	}
	if opts.PoolName != "pool-a" || opts.RunnerName != "runner-1" || opts.AccountID != "AcctOne" {
		t.Errorf("identity fields wrong: %+v", opts)
	}
	if opts.MachineType != "e2-medium" || opts.VMImageConfig.ImageName != "projects/p/global/images/img" ||
		len(opts.Zones) != 1 || opts.Zones[0] != "us-central1-a" {
		t.Errorf("machine/image/zone fields wrong: %+v", opts)
	}
	if opts.TenantID != types.DefaultTenantID || opts.DriverName != "google" {
		t.Errorf("tenant/driver wrong: tenant=%q driver=%q", opts.TenantID, opts.DriverName)
	}
}

func assertPreparedLabels(t *testing.T, opts *types.InstanceCreateOpts) {
	t.Helper()
	if opts.VMLabels["pool_id"] != "pool-a" ||
		opts.VMLabels["source"] != string(types.InstanceSourceOnDemand) ||
		opts.VMLabels["harness_env"] != "qa" ||
		opts.VMLabels[LabelAccountID] != "acctone" ||
		opts.VMLabels[LabelStageExecutionID] != "stage1" ||
		opts.VMLabels[LabelPipelineExecutionID] != "exec1" {
		t.Errorf("identity labels wrong: %v", opts.VMLabels)
	}
	if len(opts.CACert) != 0 || len(opts.TLSCert) != 0 || len(opts.TLSKey) != 0 {
		t.Error("lite-engine certificates must stay empty")
	}
}

func TestPrepareEphemeralCreateOptsPlatformFallback(t *testing.T) {
	in := ephemeralInput(ephemeralPool(&config.Google{}))
	opts, err := PrepareEphemeralCreateOpts(&in)
	if err != nil {
		t.Fatalf("PrepareEphemeralCreateOpts: %v", err)
	}
	if opts.Platform.OS != "linux" || opts.Platform.Arch != "amd64" {
		t.Errorf("platform = %+v", opts.Platform)
	}
}

func TestPrepareEphemeralCreateOptsEgress(t *testing.T) {
	in := ephemeralInput(ephemeralPool(&config.Google{EgressControl: true}))
	in.EgressCACert = "CA-PEM"
	in.ProxyUser = "user"
	in.ProxyPassword = "pass"
	opts, err := PrepareEphemeralCreateOpts(&in)
	if err != nil {
		t.Fatalf("PrepareEphemeralCreateOpts: %v", err)
	}
	if opts.EgressCACert != "CA-PEM" || opts.EgressProxyUser != "user" || opts.EgressProxyPassword != "pass" {
		t.Errorf("egress fields not carried over: %+v", opts)
	}
}

func TestPrepareEphemeralCreateOptsCopiesZones(t *testing.T) {
	in := ephemeralInput(ephemeralPool(&config.Google{}))
	opts, err := PrepareEphemeralCreateOpts(&in)
	if err != nil {
		t.Fatalf("PrepareEphemeralCreateOpts: %v", err)
	}
	opts.Zones[0] = "changed"
	if in.Zones[0] != "us-central1-a" {
		t.Error("opts.Zones aliases the input slice")
	}
}

// namedDriver is a google mock whose DriverName can be any supported driver.
type namedDriver struct {
	googleMockDriver
	name string
}

func (n *namedDriver) DriverName() string { return n.name }

func TestPrepareEphemeralCreateOptsNonGoogleDrivers(t *testing.T) {
	for _, name := range []string{string(types.Amazon), string(types.Azure), string(types.DigitalOcean)} {
		name := name
		t.Run(name, func(t *testing.T) {
			pool := ephemeralPool(nil)
			pool.Driver = &namedDriver{name: name}
			pool.Spec = &config.Amazon{}
			in := ephemeralInput(pool)
			opts, err := PrepareEphemeralCreateOpts(&in)
			if err != nil {
				t.Fatalf("PrepareEphemeralCreateOpts: %v", err)
			}
			if !opts.EphemeralDelegate || opts.DriverName != name {
				t.Fatalf("opts driver=%q ephemeral=%v", opts.DriverName, opts.EphemeralDelegate)
			}
			if opts.EgressCACert != "" {
				t.Errorf("egress CA = %q, want empty", opts.EgressCACert)
			}
		})
	}
}

func TestPinBinaryDownloadsToHarnessCoversDelegate(t *testing.T) {
	t.Run("delegate path is rebased and fallback collapses onto it", func(t *testing.T) {
		opts := &types.InstanceCreateOpts{
			DelegatePath:         "https://mirror.example.com/delegates/3.9.0",
			DelegateFallbackPath: "https://other.example.com/delegates/3.9.0",
		}
		PinBinaryDownloadsToHarness(opts)
		want := "https://app.harness.io/storage/harness-download/delegates/3.9.0"
		if opts.DelegatePath != want || opts.DelegateFallbackPath != want {
			t.Errorf("delegate paths = %q / %q, want both %q", opts.DelegatePath, opts.DelegateFallbackPath, want)
		}
	})

	t.Run("empty delegate path stays empty", func(t *testing.T) {
		opts := &types.InstanceCreateOpts{}
		PinBinaryDownloadsToHarness(opts)
		if opts.DelegatePath != "" || opts.DelegateFallbackPath != "" {
			t.Errorf("delegate paths = %q / %q, want empty", opts.DelegatePath, opts.DelegateFallbackPath)
		}
	})
}
