{{- define "currency-mcp.fullname" -}}
{{- printf "%s-currency-mcp" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "currency-mcp.selectorLabels" -}}
app.kubernetes.io/name: currency-mcp
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "currency-mcp.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "currency-mcp.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
