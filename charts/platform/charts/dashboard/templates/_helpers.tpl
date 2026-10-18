{{- define "dashboard.fullname" -}}
{{- printf "%s-dashboard" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "dashboard.selectorLabels" -}}
app.kubernetes.io/name: dashboard
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "dashboard.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "dashboard.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
