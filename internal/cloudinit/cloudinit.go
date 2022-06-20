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

	"github.com/drone-runners/drone-runner-aws/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Params defines parameters used to create userdata files.
type Params struct {
	LiteEnginePath string
	CACert         string
	TLSCert        string
	TLSKey         string
	Platform       types.Platform
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

const macScript = `
#!/usr/bin/env bash
if (! /usr/local/bin/docker stats --no-stream ); then
  # On Mac OS this would be the terminal command to launch Docker
  open /Applications/Docker.app
 #Wait until Docker daemon is running and has completed initialisation
while (! /usr/local/bin/docker stats --no-stream ); do
  # Docker takes a few seconds to initialize
  echo "Waiting for Docker to launch..."
  sleep 1
done
fi
mkdir /tmp/certs/

echo {{ .CACert | base64 }} | base64 -d >> {{ .CaCertPath }}
chmod 0600 {{ .CaCertPath }}

echo {{ .TLSCert | base64 }} | base64 -d  >> {{ .CertPath }}
chmod 0600 {{ .CertPath }}

echo {{ .TLSKey | base64 }} | base64 -d >> {{ .KeyPath }}
chmod 0600 {{ .KeyPath }}

/usr/local/bin/wget "{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /usr/local/bin/lite-engine
chmod 777 /usr/local/bin/lite-engine
touch $HOME/.env
/usr/local/bin/lite-engine server --env-file $HOME/.env > $HOME/lite-engine.log 2>&1 &
`

const macArm64Script = `
#!/usr/bin/env bash
mkdir /tmp/certs/

echo {{ .CACert | base64 }} | base64 -d >> {{ .CaCertPath }}
chmod 0600 {{ .CaCertPath }}

echo {{ .TLSCert | base64 }} | base64 -d  >> {{ .CertPath }}
chmod 0600 {{ .CertPath }}

echo {{ .TLSKey | base64 }} | base64 -d >> {{ .KeyPath }}
chmod 0600 {{ .KeyPath }}

wget "{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /opt/homebrew/bin/lite-engine
chmod 777 /opt/homebrew/bin/lite-engine
touch $HOME/.env
echo -e "SkipPrepareServer=true" >> .env;
/opt/homebrew/bin/lite-engine server --env-file $HOME/.env > $HOME/lite-engine.log 2>&1 &
`

var macTemplate = template.Must(template.New("mac").Funcs(funcs).Parse(macScript))
var macArm64Template = template.Must(template.New("mac-arm64").Funcs(funcs).Parse(macArm64Script))

func Mac(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	var p = struct {
		Params
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}

	if params.Platform.Arch == oshelp.ArchARM64 {
		err := macArm64Template.Execute(sb, p)
		if err != nil {
			err = fmt.Errorf("failed to execute mac arm64 template to get init script: %w", err)
			panic(err)
		}
	} else {
		err := macTemplate.Execute(sb, p)
		if err != nil {
			err = fmt.Errorf("failed to execute mac amd64 template to get init script: %w", err)
			panic(err)
		}
	}
	return sb.String()
}

const ubuntuScript = `
#cloud-config
apt:
  sources:
    docker.list:
      source: deb [arch={{ .Platform.Arch }}] https://download.docker.com/linux/ubuntu $RELEASE stable
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
- 'ufw allow 9079'
- 'wget "{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /usr/bin/lite-engine'
- 'chmod 777 /usr/bin/lite-engine'
- 'touch /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > /var/log/lite-engine.log 2>&1 &'`

var ubuntuTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(ubuntuScript))

const amazonLinuxScript = `
#cloud-config
packages:
- wget
- docker
- git
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
- 'sudo service docker start'
- 'sudo usermod -a -G docker ec2-user'
- 'wget "{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /usr/bin/lite-engine'
- 'chmod 777 /usr/bin/lite-engine'
- 'touch /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > /var/log/lite-engine.log 2>&1 &'`

var amazonLinuxTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(amazonLinuxScript))

// Linux creates a userdata file for the Linux operating system.
func Linux(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")
	switch params.Platform.OSName {
	case oshelp.AmazonLinux:
		err := amazonLinuxTemplate.Execute(sb, struct {
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
	default:
		// Ubuntu
		err := ubuntuTemplate.Execute(sb, struct {
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
	}

	return sb.String()
}

const windowsScript = `
<powershell>

echo "[DRONE] Initialization Starting"

echo "[DRONE] Installing Scoop Package Manager"
iex "& {$(irm get.scoop.sh)} -RunAsAdmin"

echo "[DRONE] Installing Git"
scoop install git --global

echo "[DRONE] Updating PATH so we have access to git commands (otherwise Scoop.sh shim files cannot be found)"
$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")

echo "[DRONE] Setup LiteEngine Certificates"

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

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls11 -bor [Net.SecurityProtocolType]::Tls

# Refresh the PSEnviroment
refreshenv

fsutil file createnew "C:\Program Files\lite-engine\.env" 0
Invoke-WebRequest -Uri "{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}.exe" -OutFile "C:\Program Files\lite-engine\lite-engine.exe"
New-NetFirewallRule -DisplayName "ALLOW TCP PORT 9079" -Direction inbound -Profile Any -Action Allow -LocalPort 9079 -Protocol TCP
Start-Process -FilePath "C:\Program Files\lite-engine\lite-engine.exe" -ArgumentList "server --env-file=` + "`" + `"C:\Program Files\lite-engine\.env` + "`" + `"" -RedirectStandardOutput "C:\Program Files\lite-engine\log.out" -RedirectStandardError "C:\Program Files\lite-engine\log.err"

echo "[DRONE] Initialization Complete"

</powershell>`

var windowsTemplate = template.Must(template.New(oshelp.OSWindows).Funcs(funcs).Parse(windowsScript))

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
