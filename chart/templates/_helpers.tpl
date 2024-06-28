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

{{/* Generates Postgres Connection string
USAGE:
{{ include "harnesscommon.dbconnectionv2.postgresConnection" (dict "ctx" $ "database" "foo" "args" "bar" "keywordValueConnectionString" true) }}
*/}}
{{- define "harnesscommon.dbconnectionv2.postgresConnection" }}
    {{- $ := .ctx }}
    {{- $type := "postgres" }}
    {{- $dbType := upper $type }}
    {{- $installed := true }}
    {{- if eq $.Values.global.database.postgres.installed false }}
        {{- $installed = false }}
    {{- end }}
    {{- if eq $.Values.postgres.enabled true }}
        {{- $installed = false }}
    {{- end }}
    {{- $hosts := list }}
    {{- if gt (len $.Values.postgres.hosts) 0 }}
        {{- $hosts = $.Values.postgres.hosts }}
    {{- else }}
        {{- $hosts = $.Values.global.database.postgres.hosts }}
    {{- end }}
    {{- $keywordValueConnectionString := .keywordValueConnectionString }}
    {{- $protocol := (include "harnesscommon.precedence.getValueFromKey" (dict "ctx" $ "valueType" "string" "keys" (list ".Values.global.database.postgres.protocol" ".Values.postgres.protocol"))) }}
    {{- $extraArgs := (include "harnesscommon.precedence.getValueFromKey" (dict "ctx" $ "valueType" "string" "keys" (list ".Values.global.database.postgres.extraArgs" ".Values.postgres.extraArgs"))) }}
    {{- $userVariableName := default (printf "%s_USER" $dbType) .userVariableName }}
    {{- $passwordVariableName := default (printf "%s_PASSWORD" $dbType) .passwordVariableName }}
    {{- $sslMode := default "disable" (include "harnesscommon.precedence.getValueFromKey" (dict "ctx" $ "valueType" "string" "keys" (list ".Values.global.database.postgres.sslMode" ".Values.postgres.sslMode"))) }}
    {{- if $installed }}
        {{- if $keywordValueConnectionString }}
            {{- $connectionString := (printf " host=%s user=%s password=%s dbname=%s sslmode=%s%s" "postgres" $userVariableName $passwordVariableName .database $sslMode $extraArgs) }}
            {{- printf "%s" $connectionString }}
        {{- else }}
            {{- $connectionString := (printf "%s://$(%s):$(%s)@%s/%s?%s" "postgres" $userVariableName $passwordVariableName "postgres:5432" .database .args) }}
            {{- printf "%s" $connectionString }}
        {{- end }}
    {{- else }}
        {{- $paramArgs := default "" .args }}
        {{- $finalArgs := (printf "/%s" .database) }}
        {{- if and $paramArgs $extraArgs }}
            {{- $finalArgs = (printf "%s?%s&%s" $finalArgs $paramArgs $extraArgs) }}
        {{- else if or $paramArgs $extraArgs }}
            {{- $finalArgs = (printf "%s?%s" $finalArgs (default $paramArgs $extraArgs)) }}
        {{- end }}
        {{- $firsthostport := (index $hosts 0) -}}
        {{- $hostport := split ":" $firsthostport -}}
        {{- if $keywordValueConnectionString }}
            {{- $connectionString := (printf " host=%s user=%s password=%s dbname=%s sslmode=%s%s" $hostport._0 $userVariableName $passwordVariableName .database $sslMode $extraArgs) }}
            {{- printf "%s" $connectionString }}
        {{- else }}
            {{- include "harnesscommon.dbconnection.connection" (dict "type" $type "hosts" $hosts "protocol" $protocol "extraArgs" $finalArgs "userVariableName" $userVariableName "passwordVariableName" $passwordVariableName)}}
        {{- end }}
    {{- end }}
{{- end }}