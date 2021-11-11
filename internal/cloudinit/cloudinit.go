// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts
// executed by the cloud-init directive.
//nolint:lll
package cloudinit

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Params defines parameters used to create userdata files.
type Params struct {
	PublicKey               string
	LiteEnginePath          string
	SourceCertificateFolder string
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
- docker-ce
- wget
%s
runcmd:
- 'wget "%s/lite-engine" -O /usr/bin/lite-engine'
- 'chmod 777 /usr/bin/lite-engine'
- 'touch /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > /var/log/lite-engine.log 2>&1 &'`, params.PublicKey, createFilesSection(params.SourceCertificateFolder, "/tmp/certs/"), params.LiteEnginePath)
	}
	return payload
}

func readFileEncode(path string) (encodedString string, encodingErr error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	encodedString = base64.StdEncoding.EncodeToString(content)
	return encodedString, err
}

func createFilesSection(sourceFolder, targetFolder string) (filesSection string) {
	files := []string{"ca-cert.pem", "server-cert.pem", "server-key.pem"}
	filesSection = "write_files:\n"
	for file := range files {
		sourceFile := filepath.Join(sourceFolder, files[file])
		targetFile := filepath.Join(targetFolder, files[file])
		encodedString, err := readFileEncode(sourceFile)
		if err != nil {
			fmt.Println(err)
		}
		filesSection += fmt.Sprintf(
			`- path: %s
  permissions: '0600'
  encoding: b64
  content: %s
`, targetFile, encodedString)
	}
	return filesSection
}

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
		gitKeysInstall := fmt.Sprintf(`<powershell>
Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))
choco install git.install nssm -r -y
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Set-Service -Name sshd -StartupType ‘Automatic’
Start-Service sshd
$key = "%s"
$key | Set-Content C:\ProgramData\ssh\administrators_authorized_keys
$acl = Get-Acl C:\ProgramData\ssh\administrators_authorized_keys
$acl.SetAccessRuleProtection($true, $false)
$acl.Access | `, strings.TrimSuffix(params.PublicKey, "\n"))
		adminAccessSSHRestart := "%" + `{$acl.RemoveAccessRule($_)} # strip everything
$administratorRule = New-Object system.security.accesscontrol.filesystemaccessrule("Administrator","FullControl","Allow")
$acl.SetAccessRule($administratorRule)
$administratorsRule = New-Object system.security.accesscontrol.filesystemaccessrule("Administrators","FullControl","Allow")
$acl.SetAccessRule($administratorsRule)
(Get-Item 'C:\ProgramData\ssh\administrators_authorized_keys').SetAccessControl($acl)
New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -PropertyType String -Force
restart-service sshd`
		installLE := fmt.Sprintf(` 
mkdir "C:\Program Files\lite-engine"
Invoke-WebRequest -Uri "%s/lite-engine.exe" -OutFile "C:\Program Files\lite-engine\lite-engine.exe"
Start-Process "C:\Program Files\lite-engine\lite-engine.exe" certs
New-NetFirewallRule -DisplayName "ALLOW TCP PORT 9079" -Direction inbound -Profile Any -Action Allow -LocalPort 9079 -Protocol TCP
nssm.exe install lite-engine "C:\Program Files\lite-engine\lite-engine.exe" server
nssm.exe start lite-engine
</powershell>`, params.LiteEnginePath)
		payload = gitKeysInstall + adminAccessSSHRestart + installLE
	}
	return payload
}
