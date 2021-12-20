// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts executed by the cloud-init directive.
//nolint:lll
package cloudinit

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
)

// Params defines parameters used to create userdata files.
type Params struct {
	PublicKey      string
	LiteEnginePath string
	CaCertFile     string
	CertFile       string
	KeyFile        string
}

// Linux creates a userdata file for the Linux operating system.
func Linux(params *Params) (payload string) {
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
- docker-ce
`, params.PublicKey)
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
- wget
- docker-ce
%s
runcmd:
- 'wget "%s/lite-engine" -O /usr/bin/lite-engine'
- 'chmod 777 /usr/bin/lite-engine'
- 'touch /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > /var/log/lite-engine.log 2>&1 &'
`, params.PublicKey, createLinuxCertsSection(params.CaCertFile, params.CertFile, params.KeyFile, "/tmp/certs/"), params.LiteEnginePath)
	}
	return payload
}

func Windows(params *Params) (payload string) {
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
# create powershell profile

if (test-path($profile) -eq "false")
{
	new-item -path $env:windir\System32\WindowsPowerShell\v1.0\profile.ps1 -itemtype file -force
}
# Updated profile content to explicitly import Choco
$ChocoProfileValue = @'
$ChocolateyProfile = "$env:ChocolateyInstall\helpers\chocolateyProfile.psm1"
if (Test-Path($ChocolateyProfile)) {
	Import-Module "$ChocolateyProfile"
}
'@

# Write it to the $profile location
Set-Content -Path "$profile" -Value $ChocoProfileValue -Force

# Source it
. $profile

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls11 -bor [Net.SecurityProtocolType]::Tls

# install choco
$ChocoInstallPath = "$env:SystemDrive\ProgramData\Chocolatey\bin"

if (!(Test-Path($ChocoInstallPath))) {
	Set-ExecutionPolicy Bypass -Scope LocalMachine; iex ((new-object net.webclient).DownloadString('https://chocolatey.org/install.ps1'))
}

# Refresh the PSEnviroment
refreshenv

# Stop getting prompted
choco feature enable -n=allowGlobalConfirmation

# Remember Arguments when upgrading programs
choco feature enable -n=useRememberedArgumentsForUpgrades

choco install -y git

fsutil file createnew "C:\Program Files\lite-engine\.env" 0
Invoke-WebRequest -Uri "%s/lite-engine.exe" -OutFile "C:\Program Files\lite-engine\lite-engine.exe"
New-NetFirewallRule -DisplayName "ALLOW TCP PORT 9079" -Direction inbound -Profile Any -Action Allow -LocalPort 9079 -Protocol TCP
Start-Process -FilePath "C:\Program Files\lite-engine\lite-engine.exe" -ArgumentList "server --env-file=`+"`"+`"C:\Program Files\lite-engine\.env`+"`"+`"" -RedirectStandardOutput "C:\Program Files\lite-engine\log.out" -RedirectStandardError "C:\Program Files\lite-engine\log.err"
</powershell>`, params.LiteEnginePath)
		certs := createWindowsCertsSection(params.CaCertFile, params.CertFile, params.KeyFile, "/tmp/certs")
		payload = gitKeysInstall + adminAccessSSHRestart + certs + installLE
	}
	return payload
}

func createLinuxCertsSection(caCertFile, certFile, keyFile, targetFolder string) (section string) {
	section = "write_files:"
	section += fmt.Sprintf(`
- path: %s
  permissions: '0600'
  encoding: b64
  content: %s
`, filepath.Join(targetFolder, "ca-cert.pem"), base64.StdEncoding.EncodeToString([]byte(caCertFile)))
	section += fmt.Sprintf(`
- path: %s
  permissions: '0600'
  encoding: b64
  content: %s
`, filepath.Join(targetFolder, "server-cert.pem"), base64.StdEncoding.EncodeToString([]byte(certFile)))
	section += fmt.Sprintf(`
- path: %s
  permissions: '0600'
  encoding: b64
  content: %s
`, filepath.Join(targetFolder, "server-key.pem"), base64.StdEncoding.EncodeToString([]byte(keyFile)))
	return section
}

func createWindowsCertsSection(caCertFile, certFile, keyFile, targetFolder string) (section string) {
	section = fmt.Sprintf(`
mkdir "C:\Program Files\lite-engine"
mkdir "%s"
	 `, targetFolder)

	section += fmt.Sprintf(
		`$object0 = "%s"
$Object = [System.Convert]::FromBase64String($object0)
[system.io.file]::WriteAllBytes("%s",$object)
`, base64.StdEncoding.EncodeToString([]byte(caCertFile)), filepath.Join(targetFolder, "ca-cert.pem"))

	section += fmt.Sprintf(
		`$object1 = "%s"
$Object = [System.Convert]::FromBase64String($object1)
[system.io.file]::WriteAllBytes("%s",$object)
`, base64.StdEncoding.EncodeToString([]byte(certFile)), filepath.Join(targetFolder, "server-cert.pem"))

	section += fmt.Sprintf(
		`$object2 = "%s"
$Object = [System.Convert]::FromBase64String($object2)
[system.io.file]::WriteAllBytes("%s",$object)
`, base64.StdEncoding.EncodeToString([]byte(keyFile)), filepath.Join(targetFolder, "server-key.pem"))

	return section
}
