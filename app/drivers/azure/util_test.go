package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
)

func TestTempdir(t *testing.T) {
	tests := []struct {
		name    string
		inputOS string
		want    string
	}{
		{
			name:    "Windows temp directory",
			inputOS: oshelp.OSWindows,
			want:    "C:\\Windows\\Temp\\azure",
		},
		{
			name:    "Linux temp directory",
			inputOS: oshelp.OSLinux,
			want:    "/tmp/azure",
		},
		{
			name:    "Darwin temp directory",
			inputOS: "darwin",
			want:    "/tmp/azure",
		},
		{
			name:    "Unknown OS defaults to Unix-style",
			inputOS: "unknown",
			want:    "/tmp/azure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tempdir(tt.inputOS)
			if got != tt.want {
				t.Errorf("tempdir(%s) = %v, want %v", tt.inputOS, got, tt.want)
			}
		})
	}
}

func TestDefaultConstants(t *testing.T) {
	// Test that default constants are set correctly
	if defaultResourceGroup != "harness-runner-resource" {
		t.Errorf("defaultResourceGroup = %v, want %v", defaultResourceGroup, "harness-runner-resource")
	}
	if defaultVirtualNetworkAddress != "10.1.0.0/16" {
		t.Errorf("defaultVirtualNetworkAddress = %v, want %v", defaultVirtualNetworkAddress, "10.1.0.0/16")
	}
	if defaultSubnetAddress != "10.1.10.0/24" {
		t.Errorf("defaultSubnetAddress = %v, want %v", defaultSubnetAddress, "10.1.10.0/24")
	}
}

func TestGetPrivateIPFromInterface(t *testing.T) {
	c := &config{}
	ipAddr := "10.0.0.5"

	tests := []struct {
		name string
		nic  *armnetwork.Interface
		want string
	}{
		{
			name: "nil interface",
			nic:  nil,
			want: "",
		},
		{
			name: "nil properties",
			nic:  &armnetwork.Interface{Properties: nil},
			want: "",
		},
		{
			name: "empty IP configurations",
			nic: &armnetwork.Interface{
				Properties: &armnetwork.InterfacePropertiesFormat{
					IPConfigurations: []*armnetwork.InterfaceIPConfiguration{},
				},
			},
			want: "",
		},
		{
			name: "nil IP config element (slice contains nil pointer)",
			nic: &armnetwork.Interface{
				Properties: &armnetwork.InterfacePropertiesFormat{
					IPConfigurations: []*armnetwork.InterfaceIPConfiguration{nil},
				},
			},
			want: "",
		},
		{
			name: "nil IP config properties",
			nic: &armnetwork.Interface{
				Properties: &armnetwork.InterfacePropertiesFormat{
					IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
						{Properties: nil},
					},
				},
			},
			want: "",
		},
		{
			name: "nil private IP address",
			nic: &armnetwork.Interface{
				Properties: &armnetwork.InterfacePropertiesFormat{
					IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
						{
							Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
								PrivateIPAddress: nil,
							},
						},
					},
				},
			},
			want: "",
		},
		{
			name: "valid private IP address",
			nic: &armnetwork.Interface{
				Properties: &armnetwork.InterfacePropertiesFormat{
					IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
						{
							Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
								PrivateIPAddress: &ipAddr,
							},
						},
					},
				},
			},
			want: "10.0.0.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.getPrivateIPFromInterface(tt.nic)
			if got != tt.want {
				t.Errorf("getPrivateIPFromInterface() = %v, want %v", got, tt.want)
			}
		})
	}
}
