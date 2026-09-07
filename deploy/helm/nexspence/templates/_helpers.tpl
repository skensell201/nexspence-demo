{{/*
Expand the name of the chart.
*/}}
{{- define "nexspence.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "nexspence.fullname" -}}
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
Create chart label.
*/}}
{{- define "nexspence.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "nexspence.labels" -}}
helm.sh/chart: {{ include "nexspence.chart" . }}
{{ include "nexspence.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "nexspence.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nexspence.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "nexspence.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "nexspence.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
PostgreSQL DSN — either external or bitnami sub-chart.
*/}}
{{- define "nexspence.databaseDSN" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgres://%s:%s@%s-postgresql:5432/%s?sslmode=disable"
    .Values.postgresql.auth.username
    .Values.postgresql.auth.password
    .Release.Name
    .Values.postgresql.auth.database }}
{{- else }}
{{- .Values.externalDatabase.dsn }}
{{- end }}
{{- end }}

{{/*
Redis mutual-exclusion guard — bundled and external are not both allowed.
*/}}
{{- define "nexspence.redisInUse" -}}
{{- if and .Values.redis.enabled .Values.externalRedis.enabled }}
{{- fail "only one of redis.enabled / externalRedis.enabled may be true" }}
{{- end }}
{{- end }}

{{- define "nexspence.redisAddr" -}}
{{- include "nexspence.redisInUse" . }}
{{- if .Values.redis.enabled }}
{{- printf "%s-redis-master:6379" .Release.Name }}
{{- else }}
{{- .Values.externalRedis.addr }}
{{- end }}
{{- end }}

{{- define "nexspence.redisPassword" -}}
{{- if .Values.redis.enabled }}
{{- .Values.redis.auth.password }}
{{- else }}
{{- .Values.externalRedis.password }}
{{- end }}
{{- end }}

{{- define "nexspence.redisDB" -}}
{{- if .Values.redis.enabled }}
{{- 0 }}
{{- else }}
{{- .Values.externalRedis.db }}
{{- end }}
{{- end }}

{{/*
The port the server actually listens on, taken from config.httpAddr
(":8081", "0.0.0.0:8081"). The containerPort, both probes and the Service
read it from here, so changing httpAddr moves all of them together instead
of leaving the pod pointing at a port nothing serves.
*/}}
{{- define "nexspence.httpPort" -}}
{{- $listen := default ":8081" .Values.config.httpAddr -}}
{{- $port := last (splitList ":" $listen) -}}
{{- if not (regexMatch "^[0-9]+$" $port) -}}
{{- fail (printf "config.httpAddr %q has no port; expected something like \":8081\"" $listen) -}}
{{- end -}}
{{- $port -}}
{{- end }}
