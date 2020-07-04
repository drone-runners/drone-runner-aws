// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts
// executed by the cloud-init directive.
package userdata

import "fmt"

// Params defines parameters used to create userdata files.
type Params struct {
	PublicKey string
}

// Linux creates a userdata file for the Linux operating system.
func Linux(params Params) string {
	return fmt.Sprintf(`#cloud-config
system_info:
  default_user: ~
users:
- name: root
  sudo: ALL=(ALL) NOPASSWD:ALL
  ssh-authorized-keys:
  - ssh-rsa %s drone@localhost	
`, params.PublicKey)
}

// Windows creates a userdata file for the Windows operating system.
func Windows() string {
	return ""
}
