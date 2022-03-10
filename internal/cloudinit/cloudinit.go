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
	"text/template"
)

// Params defines parameters used to create userdata files.
type Params struct {
	LiteEnginePath string
	CACert         string
	TLSCert        string
	TLSKey         string
	Platform       string
	Architecture   string
}

var funcs = map[string]interface{}{
	"base64": func(src string) string {
		return base64.StdEncoding.EncodeToString([]byte(src))
	},
	"trim": strings.TrimSpace,
}

const certsDir = "/tmp/certs/"

// Custom creates a custom userdata file.
func Custom(templateText string, params *Params) (payload string, err error) {
	t, err := template.New("custom-template").Funcs(funcs).Parse(templateText)
	if err != nil {
		err = fmt.Errorf("failed to parse template data: %w", err)
		return
	}

	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	err = t.Execute(sb, struct {
		Params
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	})
	if err != nil {
		err = fmt.Errorf("failed to execute template to get init script: %w", err)
		return
	}

	payload = sb.String()

	return
}

const linuxScript = `
#cloud-config
apt:
  sources:
    docker.list:
      source: deb [arch={{ .Architecture }}] https://download.docker.com/linux/ubuntu $RELEASE stable
      keyid: 9DC858229FC7DD38854AE2D88D81803C0EBFCD88
packages:
- wget
- docker-ce
write_files:
- path: {{ .CaCertPath }}
  permissions: '0600'
  encoding: b64
  content: {{ .CACert | base64  }}
- path: {{ .CertPath }}
  permissions: '0600'
  encoding: b64
  content: {{ .TLSCert | base64 }}
- path: {{ .KeyPath }}
  permissions: '0600'
  encoding: b64
  content: {{ .TLSKey | base64 }}
runcmd:
- 'wget "{{ .LiteEnginePath }}/lite-engine-{{ .Platform }}-{{ .Architecture }}" -O /usr/bin/lite-engine'
- 'chmod 777 /usr/bin/lite-engine'
- 'touch /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > /var/log/lite-engine.log 2>&1 &'`

var linuxTemplate = template.Must(template.New("linux").Funcs(funcs).Parse(linuxScript))

// Linux creates a userdata file for the Linux operating system.
func Linux(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	err := linuxTemplate.Execute(sb, struct {
		Params
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	})
	if err != nil {
		panic(err)
	}

	return sb.String()
}

const windowsScript = `
<powershell>
Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))
choco install -y git

# certificates

mkdir "C:\Program Files\lite-engine"
mkdir "{{ .CertDir }}"

$object0 = "{{ .CACert | base64 }}"
$Object = [System.Convert]::FromBase64String($object0)
[system.io.file]::WriteAllBytes("{{ .CaCertPath }}",$object)

$object1 = "{{ .TLSCert | base64 }}"
$Object = [System.Convert]::FromBase64String($object1)
[system.io.file]::WriteAllBytes("{{ .CertPath }}",$object)

$object2 = "{{ .TLSKey | base64 }}"
$Object = [System.Convert]::FromBase64String($object2)
[system.io.file]::WriteAllBytes("{{ .KeyPath }}",$object)

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
Invoke-WebRequest -Uri "{{ .LiteEnginePath }}/lite-engine-{{ .Platform }}-{{ .Architecture }}.exe" -OutFile "C:\Program Files\lite-engine\lite-engine.exe"
New-NetFirewallRule -DisplayName "ALLOW TCP PORT 9079" -Direction inbound -Profile Any -Action Allow -LocalPort 9079 -Protocol TCP
Start-Process -FilePath "C:\Program Files\lite-engine\lite-engine.exe" -ArgumentList "server --env-file=` + "`" + `"C:\Program Files\lite-engine\.env` + "`" + `"" -RedirectStandardOutput "C:\Program Files\lite-engine\log.out" -RedirectStandardError "C:\Program Files\lite-engine\log.err"
</powershell>`

var windowsTemplate = template.Must(template.New("windows").Funcs(funcs).Parse(windowsScript))

// Windows creates a userdata file for the Windows operating system.
func Windows(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	_ = windowsTemplate.Execute(sb, struct {
		Params
		CertDir    string
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CertDir:    certsDir,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	})

	return sb.String()
}
