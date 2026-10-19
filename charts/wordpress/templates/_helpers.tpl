{{/*
Expand the name of the chart.
*/}}
{{- define "wordpress.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "wordpress.fullname" -}}
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
{{- end }}

{{/*
Common labels
*/}}
{{- define "wordpress.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "wordpress.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "wordpress.selectorLabels" -}}
app.kubernetes.io/name: {{ include "wordpress.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Resolve the MySQL subchart fullname (matches mysql.fullname logic)
*/}}
{{- define "wordpress.mysql.fullname" -}}
{{- if .Values.mysql.fullnameOverride -}}
{{- .Values.mysql.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default "mysql" .Values.mysql.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Resolve the MySQL hostname
*/}}
{{- define "wordpress.mysql.host" -}}
{{- if .Values.mysql.enabled -}}
{{- include "wordpress.mysql.fullname" . -}}
{{- else -}}
{{- .Values.externalDatabase.host -}}
{{- end -}}
{{- end }}

{{/*
DB env vars read by the image's wp-config.php; shared by the Deployment and the setup Job
*/}}
{{- define "wordpress.dbEnv" -}}
{{- $dbName := ternary .Values.mysql.mysql.database .Values.externalDatabase.database .Values.mysql.enabled -}}
{{- $dbUser := ternary .Values.mysql.mysql.user .Values.externalDatabase.user .Values.mysql.enabled -}}
- name: WORDPRESS_DB_HOST
  value: "{{ include "wordpress.mysql.host" . }}:3306"
- name: WORDPRESS_DB_NAME
  value: {{ $dbName | default "app_db" | quote }}
- name: WORDPRESS_DB_USER
  value: {{ $dbUser | default "app_user" | quote }}
- name: WORDPRESS_DB_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "wordpress.mysql.secretName" . }}
      key: mysql-password
{{- end }}

{{/*
Resolve the MySQL secret name
*/}}
{{- define "wordpress.mysql.secretName" -}}
{{- if .Values.mysql.enabled -}}
{{- include "wordpress.mysql.fullname" . -}}
{{- else -}}
{{- .Values.externalDatabase.existingSecret -}}
{{- end -}}
{{- end }}
