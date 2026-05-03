{{/*
Common helpers. Names follow the standard Helm pattern: every resource is
prefixed with the release name so two releases in the same namespace
don't collide.
*/}}

{{- define "pgao.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "pgao.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "pgao.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "pgao.labels" -}}
helm.sh/chart: {{ include "pgao.chart" . }}
{{ include "pgao.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "pgao.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pgao.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "pgao.serviceAccountName" -}}
{{ include "pgao.fullname" . }}
{{- end -}}

{{/*
Resolve image tag with .Chart.AppVersion fallback so .Values.image.tag
can stay empty by default.
*/}}
{{- define "pgao.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
Name of the auth secret. Either user-provided (existingSecret) or one
the chart creates from auth.apiKey.
*/}}
{{- define "pgao.authSecretName" -}}
{{- if .Values.auth.existingSecret -}}
{{ .Values.auth.existingSecret }}
{{- else -}}
{{ include "pgao.fullname" . }}-auth
{{- end -}}
{{- end -}}
