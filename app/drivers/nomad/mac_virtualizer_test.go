package nomad

import (
	"strings"
	"testing"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestGetDNSServersFromMeta(t *testing.T) {
	tests := []struct {
		name     string
		meta     map[string]string
		expected string
	}{
		{
			name:     "nil meta returns empty",
			meta:     nil,
			expected: "",
		},
		{
			name:     "empty meta returns empty",
			meta:     map[string]string{},
			expected: "",
		},
		{
			name:     "custom_dns not set returns empty",
			meta:     map[string]string{"dns_servers": "10.200.0.16,8.8.8.8"},
			expected: "",
		},
		{
			name:     "custom_dns=false returns empty",
			meta:     map[string]string{"custom_dns": "false", "dns_servers": "10.200.0.16,8.8.8.8"},
			expected: "",
		},
		{
			name:     "custom_dns=true with servers",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "10.200.0.16,8.8.8.8"},
			expected: "10.200.0.16,8.8.8.8",
		},
		{
			name:     "custom_dns=TRUE case insensitive",
			meta:     map[string]string{"custom_dns": "TRUE", "dns_servers": "10.200.0.16"},
			expected: "10.200.0.16",
		},
		{
			name:     "custom_dns=True mixed case",
			meta:     map[string]string{"custom_dns": "True", "dns_servers": "1.1.1.1,8.8.8.8"},
			expected: "1.1.1.1,8.8.8.8",
		},
		{
			name:     "custom_dns=true but dns_servers empty",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": ""},
			expected: "",
		},
		{
			name:     "custom_dns=true but dns_servers missing",
			meta:     map[string]string{"custom_dns": "true"},
			expected: "",
		},
		{
			name:     "custom_dns=true with whitespace in dns_servers",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": " 10.200.0.16,8.8.8.8 "},
			expected: "10.200.0.16,8.8.8.8",
		},
		{
			name:     "malformed dns_servers - not an IP",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "not-an-ip"},
			expected: "",
		},
		{
			name:     "malformed dns_servers - hostname instead of IP",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "dns1.example.com"},
			expected: "",
		},
		{
			name:     "malformed dns_servers - one valid one invalid",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "10.200.0.16,bad"},
			expected: "",
		},
		{
			name:     "malformed dns_servers - double comma",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "10.200.0.16,,8.8.8.8"},
			expected: "",
		},
		{
			name:     "malformed dns_servers - trailing comma",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "10.200.0.16,"},
			expected: "",
		},
		{
			name:     "valid IPv6 address",
			meta:     map[string]string{"custom_dns": "true", "dns_servers": "2001:4860:4860::8888,8.8.8.8"},
			expected: "2001:4860:4860::8888,8.8.8.8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDNSServersFromMeta(tt.meta)
			if got != tt.expected {
				t.Errorf("getDNSServersFromMeta(%v) = %q, want %q", tt.meta, got, tt.expected)
			}
		})
	}
}

func TestGenerateDNSSetupBlock(t *testing.T) {
	tests := []struct {
		name       string
		dnsServers string
		wantParts  []string
	}{
		{
			name:       "single server",
			dnsServers: "10.200.0.16",
			wantParts: []string{
				`[DNS] Custom DNS configuration detected: 10.200.0.16`,
				`networksetup -setdnsservers Ethernet 10.200.0.16`,
				`[DNS] Applying DNS servers`,
				`[DNS] DNS servers applied successfully`,
				`[DNS] Verifying DNS resolution`,
				`nslookup app.harness.io`,
				`nslookup github.com`,
			},
		},
		{
			name:       "multiple servers comma separated converts to space separated",
			dnsServers: "10.200.0.16,8.8.8.8",
			wantParts: []string{
				`networksetup -setdnsservers Ethernet 10.200.0.16 8.8.8.8`,
				`[DNS] Custom DNS configuration detected: 10.200.0.16,8.8.8.8`,
			},
		},
		{
			name:       "three servers",
			dnsServers: "10.200.0.16,10.183.0.14,8.8.8.8",
			wantParts: []string{
				`networksetup -setdnsservers Ethernet 10.200.0.16 10.183.0.14 8.8.8.8`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateDNSSetupBlock(tt.dnsServers)
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("generateDNSSetupBlock(%q) missing expected content %q", tt.dnsServers, part)
				}
			}
		})
	}
}

func TestGenerateDNSSetupBlockContainsExpectStructure(t *testing.T) {
	block := generateDNSSetupBlock("10.200.0.16,8.8.8.8")

	expectedStructures := []string{
		"expect <<- DONE",
		"set timeout 30",
		"spawn ssh",
		"StrictHostKeyChecking=no",
		`"*Password:"`,
	}

	for _, s := range expectedStructures {
		if !strings.Contains(block, s) {
			t.Errorf("DNS setup block missing expected structure %q", s)
		}
	}
}

func TestGenerateStartupScript_WithDNS(t *testing.T) {
	mv := &MacVirtualizer{
		nomadConfig: &types.NomadConfig{},
	}

	vmImageConfig := types.VMImageConfig{
		ImageName: "sonoma_14.6",
		Username:  "admin",
		Password:  "password",
	}

	resource := cf.NomadResource{
		Cpus:     "4",
		MemoryGB: "8",
		DiskSize: "100",
	}

	script := mv.generateStartupScript("test-vm", "machinePass", vmImageConfig, resource, 9079, "10.200.0.16,8.8.8.8")

	if !strings.Contains(script, "[DNS] Custom DNS configuration detected") {
		t.Error("startup script with DNS servers should contain DNS setup block")
	}
	if !strings.Contains(script, "networksetup -setdnsservers Ethernet 10.200.0.16 8.8.8.8") {
		t.Error("startup script should contain networksetup command with space-separated servers")
	}
	if !strings.Contains(script, "Tart VM Started") {
		t.Error("startup script should still contain 'Tart VM Started' marker")
	}
}

func TestGenerateStartupScript_WithoutDNS(t *testing.T) {
	mv := &MacVirtualizer{
		nomadConfig: &types.NomadConfig{},
	}

	vmImageConfig := types.VMImageConfig{
		ImageName: "sonoma_14.6",
		Username:  "admin",
		Password:  "password",
	}

	resource := cf.NomadResource{
		Cpus:     "4",
		MemoryGB: "8",
		DiskSize: "100",
	}

	script := mv.generateStartupScript("test-vm", "machinePass", vmImageConfig, resource, 9079, "")

	if strings.Contains(script, "[DNS]") {
		t.Error("startup script without DNS servers should NOT contain DNS setup block")
	}
	if strings.Contains(script, "networksetup -setdnsservers") {
		t.Error("startup script without DNS servers should NOT contain networksetup command")
	}
	if !strings.Contains(script, "Tart VM Started") {
		t.Error("startup script should contain 'Tart VM Started' marker")
	}
}

func TestGenerateStartupScript_ExistingFunctionality(t *testing.T) {
	mv := &MacVirtualizer{
		nomadConfig: &types.NomadConfig{},
	}

	vmImageConfig := types.VMImageConfig{
		ImageName: "sonoma_14.6",
		Username:  "admin",
		Password:  "password",
		VMImageAuth: types.VMImageAuth{
			Registry: "registry.example.com",
			Username: "reguser",
			Password: "regpass",
		},
	}

	resource := cf.NomadResource{
		Cpus:     "6",
		MemoryGB: "12",
		DiskSize: "390",
	}

	script := mv.generateStartupScript("vm-123", "machPass", vmImageConfig, resource, 8080, "")

	expectedParts := []string{
		`VM_IMAGE="sonoma_14.6"`,
		`VM_ID="vm-123"`,
		`VM_USER="admin"`,
		`VM_PASSWORD="password"`,
		"tart clone",
		"tart set",
		"--cpu 6 --memory 12288 --disk-size 390",
		"tart run --no-graphics",
		"tart ip",
		"Waiting for VM to get IP",
		"Stopping tart VM",
		"Re-starting tart VM",
		"Tart VM Started",
		lockFunction[:20],
	}

	for _, part := range expectedParts {
		if !strings.Contains(script, part) {
			t.Errorf("startup script missing expected content: %q", part)
		}
	}
}

func TestIsFullyQualifiedImage(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected bool
	}{
		{
			name:     "empty string",
			image:    "",
			expected: false,
		},
		{
			name:     "simple image name",
			image:    "sonoma_14.6",
			expected: false,
		},
		{
			name:     "fully qualified with registry",
			image:    "registry.example.com/org/image:tag",
			expected: true,
		},
		{
			name:     "fully qualified with port",
			image:    "localhost:5000/image",
			expected: true,
		},
		{
			name:     "path without registry",
			image:    "org/image",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFullyQualifiedImage(tt.image)
			if got != tt.expected {
				t.Errorf("isFullyQualifiedImage(%q) = %v, want %v", tt.image, got, tt.expected)
			}
		})
	}
}
