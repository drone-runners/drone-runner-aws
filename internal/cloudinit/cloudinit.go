// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts
// executed by the cloud-init directive.
package cloudinit

import (
	"fmt"
	"strings"
)

// Params defines parameters used to create userdata files.
type Params struct {
	PublicKey      string
	LiteEnginePath string
}

// Linux creates a userdata file for the Linux operating system.
func Linux(params Params) (payload string) {
	if params.LiteEnginePath == "" {
		payload = fmt.Sprintf(`#cloud-config
system_info:
  default_user: ~
users:
- default
- name: root
  sudo: ALL=(ALL) NOPASSWD:ALL
  groups: sudo
  ssh-authorized-keys:
  - %s
apt:
  sources:
    docker.list:
      source: deb [arch=amd64] https://download.docker.com/linux/ubuntu $RELEASE stable
      keyid: 9DC858229FC7DD38854AE2D88D81803C0EBFCD88
packages:
- docker-ce`, params.PublicKey)
	} else {
		payload = ""
	}
	return payload
}

//  '{"experimental": true}' | Out-File -FilePath "C:\ProgramData\docker\config\daemon.json" -encoding ASCII
//  restart-service docker

// Windows creates a userdata file for the Windows operating system.
//nolint:lll // magic powershell line
func Windows(params Params) (payload string) {
	if params.LiteEnginePath == "" {
		chunk1 := fmt.Sprintf(`<powershell>
  Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))
  choco install git.install -y
  Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
  Set-Service -Name sshd -StartupType ‘Automatic’
  Start-Service sshd
  $key = "%s"
  $key | Set-Content C:\ProgramData\ssh\administrators_authorized_keys
  $acl = Get-Acl C:\ProgramData\ssh\administrators_authorized_keys
  $acl.SetAccessRuleProtection($true, $false)
  $acl.Access | `, strings.TrimSuffix(params.PublicKey, "\n"))
		payload = chunk1 + "%" + `{$acl.RemoveAccessRule($_)} # strip everything
  $administratorRule = New-Object system.security.accesscontrol.filesystemaccessrule("Administrator","FullControl","Allow")
  $acl.SetAccessRule($administratorRule)
  $administratorsRule = New-Object system.security.accesscontrol.filesystemaccessrule("Administrators","FullControl","Allow")
  $acl.SetAccessRule($administratorsRule)
  (Get-Item 'C:\ProgramData\ssh\administrators_authorized_keys').SetAccessControl($acl)
  New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -PropertyType String -Force
  restart-service sshd
  </powershell>`
	} else {
		payload = ""
	}
	return payload
}
