{{- define "woo-mcp.fullname" -}}
{{- printf "%s-woo-mcp" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "woo-mcp.selectorLabels" -}}
app.kubernetes.io/name: woo-mcp
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "woo-mcp.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "woo-mcp.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
