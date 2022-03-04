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
		Platform platform    `json:"platform,omitempty"`
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
		Image       string            `json:"image,omitempty" yaml:"image"`
		Name        string            `json:"name,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
		Size        string            `json:"size,omitempty"`
		MachineType string            `json:"machine_type,omitempty" yaml:"machine_type"`
		UserData    string            `json:"user_data,omitempty"`
		UserDataKey string            `json:"user_data_key,omitempty"`
		Disk        disk              `json:"disk,omitempty"`
		Network     string            `json:"network,omitempty"`
		Subnetwork  string            `json:"Subnetwork,omitempty"`
		Device      device            `json:"device,omitempty"`
		PrivateIP   bool              `json:"private_ip,omitempty"`
		Zone        []string          `json:"zone,omitempty" yaml:"zone"`
		Labels      map[string]string `json:"labels,omitempty"`
		Scopes      []string          `json:"scopes,omitempty"`
	}

	// Amazon specifies the configuration for an AWS instance.
	Amazon struct {
		Account struct {
			AccessKeyID      string `json:"access_key_id,omitempty"  yaml:"access_key_id"`
			AccessKeySecret  string `json:"access_key_secret,omitempty" yaml:"access_key_secret"`
			Region           string `json:"region,omitempty"`
			AvailabilityZone string `json:"availability_zone,omitempty" yaml:"availability_zone"`
		} `json:"account,omitempty"`
		Name       string            `json:"name,omitempty"`
		Size       string            `json:"size,omitempty"`
		Tags       map[string]string `json:"tags,omitempty"`
		Type       string            `json:"type,omitempty"`
		PrivateKey string            `json:"private_key,omitempty" yaml:"private_key"`
		PublicKey  string            `json:"public_key,omitempty" yaml:"public_key"`
		UserData   string            `json:"user_data,omitempty"`
		Disk       disk              `json:"disk,omitempty"`
		Network    network           `json:"network,omitempty"`
		Device     device            `json:"device,omitempty"`
		ID         string            `json:"id,omitempty"`
		IP         string            `json:"ip,omitempty"`
		Zone       []string          `json:"zone,omitempty" yaml:"zone"`
	}

	// platform specifies the configuration for a platform instance.
	platform struct {
		OS   string `json:"os,omitempty"`
		Arch string `json:"arch,omitempty"`
	}

	// disk provides disk size and type.
	disk struct {
		Size int64  `json:"size,omitempty"`
		Type string `json:"type,omitempty"`
		Iops int64  `json:"iops,omitempty"`
	}

	// device provides the device settings.
	device struct {
		Name string `json:"name,omitempty"`
	}

	// network provides network settings.
	network struct {
		VPC               string   `json:"vpc,omitempty"`
		VPCSecurityGroups []string `json:"vpc_security_group_ids,omitempty" yaml:"vpc_security_groups"`
		SecurityGroups    []string `json:"security_groups,omitempty" yaml:"security_groups"`
		SubnetID          string   `json:"subnet_id,omitempty" yaml:"subnet_id"`
		PrivateIP         bool     `json:"private_ip,omitempty" yaml:"private_ip"`
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
	case "aws":
		s.Spec = new(Amazon)
	case "gcp":
		s.Spec = new(Google)
	default:
		return fmt.Errorf("unknown instance type %s", s.Type)
	}
	return json.Unmarshal(obj.Spec, s.Spec)
}
