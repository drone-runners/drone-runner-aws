package config

import (
	"encoding/json"
	"fmt"
)

type (
	PoolFile struct {
		Version   string     `json:"version" yaml:"version"`
		Instances []Instance `json:"instances" yaml:"instances"`
	}

	Instance struct {
		Name     string      `json:"name"`
		Default  bool        `json:"default"`
		Type     string      `json:"type"`
		Pool     int         `json:"pool"`
		Limit    int         `json:"limit"`
		Platform Platform    `json:"platform,omitempty" yaml:"platform,omitempty"`
		Spec     interface{} `json:"spec,omitempty"`
	}

	// Google specifies the configuration for a GCP instance.
	Google struct {
		Account struct {
			ProjectID           string   `json:"project_id,omitempty"  yaml:"project_id"`
			JSONPath            string   `json:"json_path,omitempty"  yaml:"json_path"`
			Scopes              []string `json:"scopes,omitempty"  yaml:"scopes"`
			ServiceAccountEmail string   `json:"service_account_email,omitempty"  yaml:"service_account_email"`
		} `json:"account,omitempty"  yaml:"account"`
		Image       string            `json:"image,omitempty" yaml:"image, omitempty"`
		Name        string            `json:"name,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
		Size        string            `json:"size,omitempty"`
		MachineType string            `json:"machine_type,omitempty" yaml:"machine_type"`
		UserData    string            `json:"user_data,omitempty"`
		UserDataKey string            `json:"user_data_key,omitempty"`
		Disk        disk              `json:"disk,omitempty"`
		Network     string            `json:"network,omitempty"`
		Subnetwork  string            `json:"Subnetwork,omitempty"`
		PrivateIP   bool              `json:"private_ip,omitempty"`
		Zone        []string          `json:"zone,omitempty" yaml:"zone"`
		Labels      map[string]string `json:"labels,omitempty"`
		Scopes      []string          `json:"scopes,omitempty"`
	}

	// Amazon specifies the configuration for an AWS instance.
	Amazon struct {
		Account       AmazonAccount     `json:"account,omitempty"`
		Name          string            `json:"name,omitempty" yaml:"name,omitempty"`
		Size          string            `json:"size,omitempty"`
		SizeAlt       string            `json:"size_alt,omitempty" yaml:"size_alt,omitempty"`
		AMI           string            `json:"ami,omitempty"`
		VPC           string            `json:"vpc,omitempty" yaml:"vpc,omitempty"`
		Tags          map[string]string `json:"tags,omitempty" yaml:"tags,omitempty"`
		Type          string            `json:"type,omitempty" yaml:"type,omitempty"`
		UserData      string            `json:"user_data,omitempty" yaml:"user_data,omitempty"`
		Disk          disk              `json:"disk,omitempty" yaml:"disk,omitempty"`
		Network       AmazonNetwork     `json:"network,omitempty" yaml:"network,omitempty"`
		DeviceName    string            `json:"device_name,omitempty" yaml:"device_name,omitempty"`
		IamProfileArn string            `json:"iam_profile_arn,omitempty" yaml:"iam_profile_arn,omitempty"`
		MarketType    string            `json:"market_type,omitempty" yaml:"market_type,omitempty"`
		RootDirectory string            `json:"root_directory,omitempty" yaml:"root_directory,omitempty"`
		Hibernate     bool              `json:"hibernate,omitempty"`
		User          string            `json:"user,omitempty" yaml:"user,omitempty"`
	}
	AmazonAccount struct {
		AccessKeyID      string `json:"access_key_id,omitempty"  yaml:"access_key_id"`
		AccessKeySecret  string `json:"access_key_secret,omitempty" yaml:"access_key_secret"`
		Region           string `json:"region,omitempty"`
		Retries          int    `json:"retries,omitempty" yaml:"retries,omitempty"`
		AvailabilityZone string `json:"availability_zone,omitempty" yaml:"availability_zone,omitempty"`
		KeyPairName      string `json:"key_pair_name,omitempty" yaml:"key_pair_name,omitempty"`
	}
	// AmazonNetwork provides AmazonNetwork settings.
	AmazonNetwork struct {
		VPC               string   `json:"vpc,omitempty"`
		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty" yaml:"vpc_security_groups"`
		SecurityGroups    []string `json:"security_groups,omitempty" yaml:"security_groups"`
		SubnetID          string   `json:"subnet_id,omitempty" yaml:"subnet_id"`
		PrivateIP         bool     `json:"private_ip,omitempty" yaml:"private_ip"`
	}

	// VMFusion specifies the configuration for a VMware instance.
	VMFusion struct {
		Account struct {
			Username string `json:"username,omitempty"  yaml:"username"`
			Password string `json:"password,omitempty"  yaml:"password"`
		}
		ISO           string `json:"iso,omitempty"`
		Name          string `json:"name,omitempty" yaml:"name"`
		Memory        int64  `json:"memory,omitempty" yaml:"memory"`
		CPU           int64  `json:"cpu,omitempty" yaml:"cpu"`
		VDiskPath     string `json:"v_disk_path,omitempty" yaml:"v_disk_path"`
		UserData      string `json:"user_data,omitempty"`
		StorePath     string `json:"store_path,omitempty" yaml:"store_path"`
		RootDirectory string `json:"root_directory,omitempty" yaml:"root_directory"`
	}
	// Anka specifies the configuration for an Anka instance.
	Anka struct {
		Account struct {
			Username string `json:"username,omitempty"  yaml:"username"`
			Password string `json:"password,omitempty"  yaml:"password"`
		}
		VMID          string `json:"vm_id,omitempty" yaml:"vm_id"`
		RootDirectory string `json:"root_directory,omitempty" yaml:"root_directory"`
		UserData      string `json:"user_data,omitempty" yaml:"user_data"`
	}

	// Platform specifies the configuration for a platform instance.
	Platform struct {
		OS      string `json:"os,omitempty"`
		Arch    string `json:"arch,omitempty"`
		Version string `json:"version,omitempty" yaml:"version,omitempty"`
	}

	// disk provides disk size and type.
	disk struct {
		Size int64  `json:"size,omitempty"`
		Type string `json:"type,omitempty"`
		Iops int64  `json:"iops,omitempty"`
	}
)

// UnmarshalJSON implement the json.Unmarshaler interface.
func (s *Instance) UnmarshalJSON(data []byte) error {
	type S Instance
	type T struct {
		*S
		Spec json.RawMessage `json:"spec"`
	}
	obj := &T{S: (*S)(s)}
	if err := json.Unmarshal(data, obj); err != nil {
		return err
	}
	switch s.Type {
	case "amazon", "aws":
		s.Spec = new(Amazon)
	case "google", "gcp":
		s.Spec = new(Google)
	case "vmfusion":
		s.Spec = new(VMFusion)
	case "anka":
		s.Spec = new(Anka)
	default:
		return fmt.Errorf("unknown instance type %s", s.Type)
	}
	return json.Unmarshal(obj.Spec, s.Spec)
}
