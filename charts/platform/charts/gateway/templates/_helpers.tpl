{{- define "gateway.fullname" -}}
{{- printf "%s-gateway" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "gateway.selectorLabels" -}}
app.kubernetes.io/name: gateway
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gateway.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "gateway.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Environment variable holding an upstream's credential, e.g. UPSTREAM_CURRENCY_CREDENTIAL. */}}
{{- define "gateway.credentialEnv" -}}
{{- printf "UPSTREAM_%s_CREDENTIAL" (. | upper | replace "-" "_") -}}
{{- end }}
