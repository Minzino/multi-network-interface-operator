{{- define "viola-test-api.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "viola-test-api.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "viola-test-api.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "viola-test-api.labels" -}}
app.kubernetes.io/name: {{ include "viola-test-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
