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

	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Params defines parameters used to create userdata files.
type Params struct {
	LiteEnginePath         string
	LiteEngineLogsPath     string
	CACert                 string
	TLSCert                string
	TLSKey                 string
	Platform               types.Platform
	HarnessTestBinaryURI   string
	PluginBinaryURI        string
	Tmate                  types.Tmate
	IsHosted               bool
	GitspaceAgentConfig    types.GitspaceAgentConfig
	StorageIdentifier      string
	AutoInjectionBinaryURI string
}

var funcs = map[string]interface{}{
	"base64": func(src string) string {
		return base64.StdEncoding.EncodeToString([]byte(src))
	},
	"trim": strings.TrimSpace,
}

const certsDir = "/tmp/certs/"
const liteEngineUsrBinPath = `"{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /usr/bin/lite-engine`
const pluginUsrBinPath = `{{ .PluginBinaryURI }}/plugin-{{ .Platform.OS }}-{{ .Platform.Arch }}  -O /usr/bin/plugin`
const pluginUsrLocalBinPath = `{{ .PluginBinaryURI }}/plugin-{{ .Platform.OS }}-{{ .Platform.Arch }}  -O /usr/local/bin/plugin`
const splitTestsUsrBinPath = `{{ .HarnessTestBinaryURI }}/{{ .Platform.Arch }}/{{ .Platform.OS }}/bin/split_tests-{{ .Platform.OS }}_{{ .Platform.Arch }} -O /usr/bin/split_tests`
const liteEngineUsrLocalBinPath = `"{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /usr/local/bin/lite-engine`
const liteEngineHomebrewBinPath = `"{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}" -O /opt/homebrew/bin/lite-engine`
const AutoInjectionUsrBinPath = `"{{ .AutoInjectionBinaryURI }}/{{ .Platform.OS }}/{{ .Platform.Arch }}/auto-injection" -O /usr/bin/auto-injection`

// Custom creates a custom userdata file.
func Custom(templateText string, params *Params) (payload string, err error) {
	t, err := template.New("custom-template").Funcs(funcs).Parse(templateText)
	if err != nil {
		err = fmt.Errorf("failed to parse template data: %w", err)
		return "", err
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
		return "", err
	}

	payload = sb.String()

	return payload, nil
}

const linuxScript = `
#!/usr/bin/bash
mkdir {{ .CertDir }}

echo {{ .CACert | base64 }} | base64 -d >> {{ .CaCertPath }}
chmod 0600 {{ .CaCertPath }}

echo {{ .TLSCert | base64 }} | base64 -d  >> {{ .CertPath }}
chmod 0600 {{ .CertPath }}

echo {{ .TLSKey | base64 }} | base64 -d >> {{ .KeyPath }}
chmod 0600 {{ .KeyPath }}

echo "setting up swap space"
fallocate -l 30G /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile
echo "done setting up swap space"

echo "downloading lite engine binary"
/usr/bin/wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + liteEngineUsrBinPath + ` || /usr/bin/wget --retry-connrefused --tries=10 --waitretry=10 -nv --debug ` + liteEngineUsrBinPath + `
echo "done downloading lite engine binary"
chmod 777 /usr/bin/lite-engine
touch $HOME/.env
cp "/etc/environment" $HOME/.env
echo "SKIP_PREPARE_SERVER=true" >> $HOME/.env;

{{ if .PluginBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + pluginUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + pluginUsrBinPath + `
chmod 777 /usr/bin/plugin
{{ end }}

{{ if .HarnessTestBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + splitTestsUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + splitTestsUsrBinPath + `
chmod 777 /usr/bin/split_tests
{{ end }}

{{ if .AutoInjectionBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + AutoInjectionUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + AutoInjectionUsrBinPath + `
chmod 777 /usr/bin/auto-injection
{{ end }}

{{ if eq .Platform.Arch "amd64" }}
curl -fL https://github.com/bitrise-io/envman/releases/download/2.4.2/envman-Linux-x86_64 > /usr/bin/envman
chmod 777 /usr/bin/envman
{{ end }}

systemctl disable docker.service
update-alternatives --set iptables /usr/sbin/iptables-legacy
echo "restarting docker"
service docker start
echo "docker service restarted"

cp /etc/resolv.conf /etc/resolv_orig.conf
rm /etc/resolv.conf
echo "nameserver 127.0.0.53" > /etc/resolv.conf 
cat /etc/resolv_orig.conf >> /etc/resolv.conf
echo "options edns0 trust-ad
search ." >> /etc/resolv.conf

{{ if .Tmate.Enabled }}
mkdir /addon
{{ if eq .Platform.Arch "amd64" }}
wget -nv https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-amd64.tar.xz  -O /addon/tmate.xz
tar -xf /addon/tmate.xz -C /addon/
chmod 777  /addon/tmate-1.0-static-linux-amd64/tmate
mv  /addon/tmate-1.0-static-linux-amd64/tmate /addon/tmate
{{ else if eq .Platform.Arch "arm64" }}
wget -nv https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-arm64v8.tar.xz -O /addon/tmate.xz
tar -xf /addon/tmate.xz -C /addon/
chmod 777  /addon/tmate-1.0-static-linux-arm64v8/tmate
mv  /addon/tmate-1.0-static-linux-arm64v8/tmate /addon/tmate
{{ end }}
{{ end }}
unlink /snap/bin/google-cloud-cli.gcloud
echo "starting lite engine server"
/usr/bin/lite-engine server --env-file $HOME/.env > {{ .LiteEngineLogsPath }} 2>&1 &
echo "done starting lite engine server"
`

const gitspacesLinuxScript = `
#!/usr/bin/bash
mkdir {{ .CertDir }}

echo {{ .CACert | base64 }} | base64 -d >> {{ .CaCertPath }}
chmod 0600 {{ .CaCertPath }}

echo {{ .TLSCert | base64 }} | base64 -d  >> {{ .CertPath }}
chmod 0600 {{ .CertPath }}

echo {{ .TLSKey | base64 }} | base64 -d >> {{ .KeyPath }}
chmod 0600 {{ .KeyPath }}

echo "setting up swap space"
fallocate -l 30G /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile
echo "done setting up swap space"

echo "downloading lite engine binary"
/usr/bin/wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + liteEngineUsrBinPath + ` || /usr/bin/wget --retry-connrefused --tries=10 --waitretry=10 -nv --debug ` + liteEngineUsrBinPath + `
echo "done downloading lite engine binary"
chmod 777 /usr/bin/lite-engine
touch $HOME/.env
cp "/etc/environment" $HOME/.env
echo "SKIP_PREPARE_SERVER=true" >> $HOME/.env;

{{ if .PluginBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + pluginUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + pluginUsrBinPath + `
chmod 777 /usr/bin/plugin
{{ end }}

{{ if .HarnessTestBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + splitTestsUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + splitTestsUsrBinPath + `
chmod 777 /usr/bin/split_tests
{{ end }}

{{ if .AutoInjectionBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + AutoInjectionUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + AutoInjectionUsrBinPath + `
chmod 777 /usr/bin/auto-injection
{{ end }}

{{ if eq .Platform.Arch "amd64" }}
curl -fL https://github.com/bitrise-io/envman/releases/download/2.4.2/envman-Linux-x86_64 > /usr/bin/envman
chmod 777 /usr/bin/envman
{{ end }}

systemctl disable docker.service
update-alternatives --set iptables /usr/sbin/iptables-legacy
echo "restarting docker"
service docker start
echo "docker service restarted"

cp /etc/resolv.conf /etc/resolv_orig.conf
rm /etc/resolv.conf
echo "nameserver 127.0.0.53" > /etc/resolv.conf 
cat /etc/resolv_orig.conf >> /etc/resolv.conf
echo "options edns0 trust-ad
search ." >> /etc/resolv.conf

{{ if .Tmate.Enabled }}
mkdir /addon
{{ if eq .Platform.Arch "amd64" }}
wget -nv https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-amd64.tar.xz  -O /addon/tmate.xz
tar -xf /addon/tmate.xz -C /addon/
chmod 777  /addon/tmate-1.0-static-linux-amd64/tmate
mv  /addon/tmate-1.0-static-linux-amd64/tmate /addon/tmate
{{ else if eq .Platform.Arch "arm64" }}
wget -nv https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-arm64v8.tar.xz -O /addon/tmate.xz
tar -xf /addon/tmate.xz -C /addon/
chmod 777  /addon/tmate-1.0-static-linux-arm64v8/tmate
mv  /addon/tmate-1.0-static-linux-arm64v8/tmate /addon/tmate
{{ end }}
{{ end }}
unlink /snap/bin/google-cloud-cli.gcloud
echo "starting lite engine server"
/usr/bin/lite-engine server --env-file $HOME/.env > {{ .LiteEngineLogsPath }} 2>&1 &
echo "done starting lite engine server"

groupadd docker
mkdir -p /opt/gitspaceagent

echo "downloading gitspaces agent binary"
echo HARNESS_JWT_SECRET={{ .GitspaceAgentConfig.Secret }} >> /etc/profile
export HARNESS_JWT_SECRET={{ .GitspaceAgentConfig.Secret }}
curl -X GET -H "Authorization: Bearer {{ .GitspaceAgentConfig.AccessToken }} " -o "/opt/gitspaceagent/agent" "https://storage.googleapis.com/storage/v1/b/gitspace-agent/o/agent-bare-metal?alt=media"
chmod 755 /opt/gitspaceagent/agent
echo "done downloading gitspace agent binary"

echo "starting gitspaces agent"
export DOCKER_API_VERSION=1.41
nohup /opt/gitspaceagent/agent > /dev/null 2>&1 &
useradd -K MAIL_DIR=/dev/null gitspaceagent
usermod -aG docker gitspaceagent
echo "done starting gitspaces agent"
`

const macScript = `
#!/usr/bin/env bash
mkdir /tmp/certs/

echo {{ .CACert | base64 }} | base64 -d >> {{ .CaCertPath }}
chmod 0600 {{ .CaCertPath }}

echo {{ .TLSCert | base64 }} | base64 -d  >> {{ .CertPath }}
chmod 0600 {{ .CertPath }}

echo {{ .TLSKey | base64 }} | base64 -d >> {{ .KeyPath }}
chmod 0600 {{ .KeyPath }}

/usr/local/bin/wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + liteEngineUsrLocalBinPath + ` || /usr/local/bin/wget --retry-connrefused --tries=10 --waitretry=10 ` + liteEngineUsrLocalBinPath + `
chmod 777 /usr/local/bin/lite-engine
touch $HOME/.env
echo "SKIP_PREPARE_SERVER=true" >> .env;

{{ if .PluginBinaryURI }}
wget {{ .PluginBinaryURI }}/plugin-{{ .Platform.OS }}-{{ .Platform.Arch }}  -O /usr/bin/plugin
chmod 777 /usr/bin/plugin
{{ end }}

/usr/local/bin/lite-engine server --env-file $HOME/.env > {{ .LiteEngineLogsPath }} 2>&1 &
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

wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + liteEngineHomebrewBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + liteEngineHomebrewBinPath + `
chmod 777 /opt/homebrew/bin/lite-engine
touch $HOME/.env
echo "SKIP_PREPARE_SERVER=true" >> .env;

{{ if .PluginBinaryURI }}
wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + pluginUsrLocalBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + pluginUsrLocalBinPath + `
chmod 777 /usr/local/bin/plugin
{{ end }}

curl -fL https://github.com/bitrise-io/envman/releases/download/2.4.2/envman-Darwin-arm64 > /usr/local/bin/envman
chmod 777 /usr/local/bin/envman

/opt/homebrew/bin/lite-engine server --env-file $HOME/.env > $HOME/lite-engine.log 2>&1 &
`

var macTemplate = template.Must(template.New("mac").Funcs(funcs).Parse(macScript))
var macArm64Template = template.Must(template.New("mac-arm64").Funcs(funcs).Parse(macArm64Script))
var linuxBashTemplate = template.Must(template.New("linux-bash").Funcs(funcs).Parse(linuxScript))
var gitspacesLinuxTemplate = template.Must(template.New("linux-bash").Funcs(funcs).Parse(gitspacesLinuxScript))

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

// This generates a bash startup script for linux
func LinuxBash(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	var p = struct {
		Params
		CaCertPath string
		CertPath   string
		CertDir    string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertDir:    certsDir,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}

	var err error
	if params.GitspaceAgentConfig.Secret != "" && params.GitspaceAgentConfig.AccessToken != "" {
		err = gitspacesLinuxTemplate.Execute(sb, p)
	} else {
		err = linuxBashTemplate.Execute(sb, p)
	}
	if err != nil {
		err = fmt.Errorf("failed to execute linux bash template to get init script: %w", err)
		panic(err)
	}
	return sb.String()
}

const ubuntuScript = `
#cloud-config
{{ if and (.IsHosted) (eq .Platform.Arch "amd64") }}
packages:
  - wget
{{ else }}
apt:
  sources:
    docker.list:
      source: deb [arch={{ .Platform.Arch }}] https://download.docker.com/linux/ubuntu $RELEASE stable
      keyid: 9DC858229FC7DD38854AE2D88D81803C0EBFCD88
packages:
  - wget
  - docker-ce
{{ end }}
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
- 'set -x'
- 'ufw allow 9079'
- 'wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 -nv --debug ` + liteEngineUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 -nv --debug ` + liteEngineUsrBinPath + `'
- 'chmod 777 /usr/bin/lite-engine'
{{ if .HarnessTestBinaryURI }}
- 'wget -nv "{{ .HarnessTestBinaryURI }}/{{ .Platform.Arch }}/{{ .Platform.OS }}/bin/split_tests-{{ .Platform.OS }}_{{ .Platform.Arch }}" -O /usr/bin/split_tests'
- 'chmod 777 /usr/bin/split_tests'
{{ end }}
{{ if .PluginBinaryURI }}
- 'wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 -nv ` + pluginUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 -nv ` + pluginUsrBinPath + `'
- 'chmod 777 /usr/bin/plugin'
{{ end }}
{{ if .AutoInjectionBinaryURI }}
- 'wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 -nv ` + AutoInjectionUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 -nv ` + AutoInjectionUsrBinPath + `'
- 'chmod 777 /usr/bin/auto-injection'
{{ end }}
{{ if eq .Platform.Arch "amd64" }}
- 'curl -fL https://github.com/bitrise-io/envman/releases/download/2.4.2/envman-Linux-x86_64 > /usr/bin/envman'
- 'chmod 777 /usr/bin/envman'
{{ end }}
- 'touch /root/.env'
- '[ -f "/etc/environment" ] && cp "/etc/environment" /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > {{ .LiteEngineLogsPath }} 2>&1 &'
{{ if .Tmate.Enabled }}
- 'mkdir /addon'
{{ if eq .Platform.Arch "amd64" }}
- 'wget -nv https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-amd64.tar.xz  -O /addon/tmate.xz' 
- 'tar -xf /addon/tmate.xz -C /addon/'
- 'chmod 777  /addon/tmate-1.0-static-linux-amd64/tmate'
- 'mv  /addon/tmate-1.0-static-linux-amd64/tmate /addon/tmate'
- 'rm -rf /addon/tmate-1.0-static-linux-amd64/'
{{ else if eq .Platform.Arch "arm64" }}
- 'wget -nv https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-arm64v8.tar.xz -O /addon/tmate.xz' 
- 'tar -xf /addon/tmate.xz -C /addon/'
- 'chmod 777  /addon/tmate-1.0-static-linux-arm64v8/tmate'
- 'mv  /addon/tmate-1.0-static-linux-arm64v8/tmate /addon/tmate'
- 'rm -rf /addon/tmate-1.0-static-linux-arm64v8/'
{{ end }}
- 'rm -rf /addon/tmate.xz'
{{ end }}`

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
- 'wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + liteEngineUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + liteEngineUsrBinPath + `'
- 'chmod 777 /usr/bin/lite-engine'
{{ if .HarnessTestBinaryURI }}
- 'wget -nv "{{ .HarnessTestBinaryURI }}/{{ .Platform.Arch }}/{{ .Platform.OS }}/bin/split_tests-{{ .Platform.OS }}_{{ .Platform.Arch }}" -O /usr/bin/split_tests'
- 'chmod 777 /usr/bin/split_tests'
{{ end }}
{{ if .PluginBinaryURI }}
- 'wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 ` + pluginUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 ` + pluginUsrBinPath + `'
- 'chmod 777 /usr/bin/plugin'
{{ end }}
{{ if .AutoInjectionBinaryURI }}
- 'wget --retry-connrefused --retry-on-host-error --retry-on-http-error=503,404,429 --tries=10 --waitretry=10 -nv ` + AutoInjectionUsrBinPath + ` || wget --retry-connrefused --tries=10 --waitretry=10 -nv ` + AutoInjectionUsrBinPath + `'
- 'chmod 777 /usr/bin/auto-injection'
{{ end }}
{{ if eq .Platform.Arch "amd64" }}
- 'curl -fL https://github.com/bitrise-io/envman/releases/download/2.4.2/envman-Linux-x86_64 > /usr/bin/envman'
- 'chmod 777 /usr/bin/envman'
{{ end }}
- 'touch /root/.env'
- '[ -f "/etc/environment" ] && cp "/etc/environment" /root/.env'
- '[ -f "/root/.env" ] && ! grep -q "^HOME=" /root/.env && echo "HOME=/root" >> /root/.env'
- '/usr/bin/lite-engine server --env-file /root/.env > {{ .LiteEngineLogsPath }} 2>&1 &'
{{ if .Tmate.Enabled }}
- 'mkdir /addon'
{{ if eq .Platform.Arch "amd64" }}
- 'wget https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-amd64.tar.xz  -O /addon/tmate.xz' 
- 'tar -xf /addon/tmate.xz -C /addon/'
- 'chmod 777  /addon/tmate-1.0-static-linux-amd64/tmate'
- 'mv  /addon/tmate-1.0-static-linux-amd64/tmate /addon/tmate'
- 'rm -rf /addon/tmate-1.0-static-linux-amd64/'
{{ else if eq .Platform.Arch "arm64" }}
- 'wget https://github.com/harness/tmate/releases/download/1.0/tmate-1.0-static-linux-arm64v8.tar.xz -O /addon/tmate.xz' 
- 'tar -xf /addon/tmate.xz -C /addon/'
- 'chmod 777  /addon/tmate-1.0-static-linux-arm64v8/tmate'
- 'mv  /addon/tmate-1.0-static-linux-arm64v8/tmate /addon/tmate'
- 'rm -rf /addon/tmate-1.0-static-linux-arm64v8/'
{{ end }}
- 'rm -rf /addon/tmate.xz'
{{ end }}`

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
$ProgressPreference = 'SilentlyContinue'
echo "[DRONE] Initialization Starting"

if (${{ .IsHosted }} -eq $false) {
	echo "[DRONE] Installing Scoop Package Manager"
	iex "& {$(irm https://get.scoop.sh)} -RunAsAdmin"

	echo "[DRONE] Installing Git"
	scoop install git --global

	echo "[DRONE] Updating PATH so we have access to git commands (otherwise Scoop.sh shim files cannot be found)"
	$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")
}

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

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls11 -bor [Net.SecurityProtocolType]::Tls

Invoke-WebRequest -Uri "{{ .PluginBinaryURI }}/plugin-{{ .Platform.OS }}-{{ .Platform.Arch }}.exe" -OutFile "C:\Program Files\lite-engine\plugin.exe"
$env:Path = 'C:\Program Files\lite-engine;' + $env:Path

# Refresh the PSEnviroment
refreshenv

fsutil file createnew "C:\Program Files\lite-engine\.env" 0
Invoke-WebRequest -Uri "{{ .LiteEnginePath }}/lite-engine-{{ .Platform.OS }}-{{ .Platform.Arch }}.exe" -OutFile "C:\Program Files\lite-engine\lite-engine.exe"
New-NetFirewallRule -DisplayName "ALLOW TCP PORT 9079" -Direction inbound -Profile Any -Action Allow -LocalPort 9079 -Protocol TCP
Start-Process -FilePath "C:\Program Files\lite-engine\lite-engine.exe" -ArgumentList "server --env-file=` + "`" + `"C:\Program Files\lite-engine\.env` + "`" + `"" -RedirectStandardOutput "{{ .LiteEngineLogsPath }}" -RedirectStandardError "C:\Program Files\lite-engine\log.err"

if (${{ .IsHosted }} -eq $true) {
	netsh interface ipv4 add dnsserver "Ethernet" 8.8.8.8 index=1
	netsh interface ipv4 add dnsserver "Ethernet" 1.1.1.1 index=2
	netsh interface ipv4 add dnsserver "Ethernet" 8.8.4.4 index=3
	ipconfig /flushdns
	Write-Host "DNS server added to Ethernet interface."
} 
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
