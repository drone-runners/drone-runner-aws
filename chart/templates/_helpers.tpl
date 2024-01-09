{{/*
Expand the name of the chart.
*/}}
{{- define "dlite.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "dlite.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "dlite.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "dlite.labels" -}}
helm.sh/chart: {{ include "dlite.chart" . }}
{{ include "dlite.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "dlite.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dlite.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "dlite.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "dlite.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "dlite.pullSecrets" -}}
{{ include "common.images.pullSecrets" (dict "images" (list .Values.image ) "global" .Values.global ) }}
{{- end -}}


{{- define "dlite.generateServiceAccountSecrets" }}
    {{- $ := .ctx }}
    {{- $hasAtleastOneSecret := false }}
    {{- $localESOSecretCtxIdentifier := (include "harnesscommon.secrets.localESOSecretCtxIdentifier" (dict "ctx" $ )) }}
    {{- if eq (include "harnesscommon.secrets.isDefaultAppSecret" (dict "ctx" $ "variableName" "DLITE_GCP_SECRET_ACCOUNT")) "true" }}
    {{- $hasAtleastOneSecret = true }}
DLITE_GCP_SECRET_ACCOUNT: {{ include "harnesscommon.secrets.passwords.manage" (dict "secret" "dlite" "key" "DLITE_GCP_SECRET_ACCOUNT" "providedValues" (list "secrets.default.DLITE_GCP_SECRET_ACCOUNT") "length" 10 "context" $) }}
    {{- end }}
    {{- if not $hasAtleastOneSecret }}
{}
    {{- end }}
{{- end }}

{{- define "harnesscommon.secrets.manageESOSecretVolumes" }}
{{- $ := .ctx }}
{{- $variableName := .variableName }}
{{- $envVariableName := $variableName }}
{{- $path := .path }}
{{- if .overrideEnvName }}
  {{- $envVariableName = .overrideEnvName }}
{{- end }}
{{- $secretName := "" }}
{{- $secretKey := "" }}
{{- if .variableName }}
  {{- range .esoSecretCtxs }}
    {{- $secretCtxIdentifier := .secretCtxIdentifier }}
    {{- $secretCtx := .secretCtx }}
    {{- range $esoSecretIdx, $esoSecret := $secretCtx }}
      {{- if and $esoSecret $esoSecret.secretStore $esoSecret.secretStore.name $esoSecret.secretStore.kind }}
        {{- $remoteKeyName := (dig "remoteKeys" $variableName "name" "" .) }}
        {{- if $remoteKeyName }}
          {{- $secretName = include "harnesscommon.secrets.esoSecretName" (dict "ctx" $ "secretContextIdentifier" $secretCtxIdentifier "secretIdentifier" $esoSecretIdx) }}
          {{- $secretKey = $variableName }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}
  {{- if and $secretName $secretKey }}
- name: {{ print $envVariableName }}
  secret:
    secretName: {{ printf "%s" $secretName }}
    items: 
    - key: {{ printf "%s" $secretKey }}
      path: {{ printf "%s" $path }}
  {{- end }}
{{- end }}

{{- define "harnesscommon.secrets.manageExtKubernetesSecretVolumews" }}
{{- $ := .ctx }}
{{- $variableName := .variableName }}
{{- $envVariableName := $variableName }}
{{- $path := .path }}
{{- if .overrideEnvName }}
  {{- $envVariableName = .overrideEnvName }}
{{- end }}
{{- $secretName := "" }}
{{- $secretKey := "" }}
{{- if $variableName }}
  {{- range .extKubernetesSecretCtxs }}
    {{- range . }}
      {{- if and . .secretName .keys }}
        {{- $currSecretKey := (get .keys $variableName) }}
        {{- if and (hasKey .keys $variableName) $currSecretKey }}
          {{- $secretName = .secretName }}
          {{- $secretKey = $currSecretKey }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
  {{- if and $secretName $secretKey }}
- name: {{ print $envVariableName }}
  secret:
    secretName: {{ printf "%s" $secretName }}
    items: 
    - key: {{ printf "%s" $secretKey }}
      path: {{ printf "%s" $path }}
  {{- end }}
{{- end }}
{{- end }}




{{- define "harnesscommon.secrets.manageVolumes" }}
{{- $ := .ctx }}
{{- $variableName := .variableName }}
{{- $envVariableName := $variableName }}
{{- if .overrideEnvName }}
  {{- $envVariableName = .overrideEnvName }}
{{- end }}
{{- $defaultValue := .defaultValue }}
{{- if eq (include "harnesscommon.secrets.hasESOSecret" (dict "variableName" .variableName "esoSecretCtxs" .esoSecretCtxs)) "true" }}
{{- include "harnesscommon.secrets.manageESOSecretVolumes" (dict "ctx" $ "variableName" .variableName "overrideEnvName" .overrideEnvName "path" .path "esoSecretCtxs"  .esoSecretCtxs) }}
{{- else if eq (include "harnesscommon.secrets.hasExtKubernetesSecret" (dict "variableName" .variableName "extKubernetesSecretCtxs" .extKubernetesSecretCtxs)) "true" }}
{{- include "harnesscommon.secrets.manageExtKubernetesSecretEnv" (dict "ctx" $ "variableName" .variableName "overrideEnvName" .overrideEnvName "path" .path "extKubernetesSecretCtxs" .extKubernetesSecretCtxs) }}
{{- else }}
{{- $KubernetesSecretName := .defaultKubernetesSecretName }}

{{- $KubernetesSecretName := .defaultKubernetesSecretName }}

- name: {{ print $envVariableName }}
  secret:
    secretName: {{ print $KubernetesSecretName }}
    items:
    - key: {{ print $envVariableName }}
      path: {{ print .path }}
{{- end }}
{{- end }}