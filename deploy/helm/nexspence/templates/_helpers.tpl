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
Bundled PostgreSQL Service / StatefulSet name.

Deliberately {release}-postgres, not {release}-postgresql: the former Bitnami
sub-chart owned a StatefulSet of that second name, and Helm would patch it
in place on upgrade (spec.selector / volumeClaimTemplates are immutable).
A new name makes this a create, and leaves the Bitnami PVC available to dump.
*/}}
{{- define "nexspence.postgresql.fullname" -}}
{{- printf "%s-postgres" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "nexspence.postgresql.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nexspence.name" . }}-postgres
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: postgresql
{{- end }}

{{/*
Secret that holds the bundled Postgres password.
*/}}
{{- define "nexspence.postgresql.secretName" -}}
{{- if .Values.postgresql.auth.existingSecret }}
{{- .Values.postgresql.auth.existingSecret }}
{{- else }}
{{- include "nexspence.postgresql.fullname" . }}
{{- end }}
{{- end }}

{{- define "nexspence.postgresql.secretPasswordKey" -}}
{{- if .Values.postgresql.auth.existingSecret -}}
{{- default "postgres-password" .Values.postgresql.auth.existingSecretPasswordKey -}}
{{- else -}}
postgres-password
{{- end -}}
{{- end }}

{{/*
PostgreSQL DSN — bundled in-chart Postgres or an operator-supplied DSN.
The bundled DSN intentionally omits the password: the Deployment exposes it
through PGPASSWORD, which pgx reads without URI encoding or a cluster lookup.
The username is URL-encoded; urlquery emits "+" for spaces, but pgx treats
that as a literal plus in URI userinfo, so replace it with "%20".
*/}}
{{- define "nexspence.databaseDSN" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgres://%s@%s:5432/%s?sslmode=disable"
    (.Values.postgresql.auth.username | urlquery | replace "+" "%20")
    (include "nexspence.postgresql.fullname" .)
    .Values.postgresql.auth.database }}
{{- else }}
{{- .Values.externalDatabase.dsn }}
{{- end }}
{{- end }}
