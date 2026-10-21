{{- define "provisioner.fullname" -}}
{{- printf "%s-provisioner" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "provisioner.selectorLabels" -}}
app.kubernetes.io/name: provisioner
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "provisioner.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "provisioner.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
