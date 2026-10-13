{{/*
The store runs two components, wordpress and mysql. Each gets its own name and selector labels;
the names are fixed rather than derived from .Chart.Name because Deployment and StatefulSet
selectors are immutable once a release exists.
*/}}

{{- define "store.componentFullname" -}}
{{- $root := index . 0 -}}
{{- $component := index . 1 -}}
{{- if contains $component $root.Release.Name -}}
{{- $root.Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" $root.Release.Name $component | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "store.chartLabel" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "wordpress.fullname" -}}
{{- include "store.componentFullname" (list . "wordpress") -}}
{{- end }}

{{- define "wordpress.selectorLabels" -}}
app.kubernetes.io/name: wordpress
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "wordpress.labels" -}}
{{ include "store.chartLabel" . }}
{{ include "wordpress.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "mysql.fullname" -}}
{{- include "store.componentFullname" (list . "mysql") -}}
{{- end }}

{{- define "mysql.selectorLabels" -}}
app.kubernetes.io/name: mysql
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "mysql.labels" -}}
{{ include "store.chartLabel" . }}
{{ include "mysql.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "wordpress.dbHost" -}}
{{- ternary (include "mysql.fullname" .) .Values.externalDatabase.host .Values.mysql.enabled -}}
{{- end }}

{{/*
DB env vars read by the wordpress image's wp-config.php; shared by the Deployment and the setup Job.
*/}}
{{- define "wordpress.dbEnv" -}}
{{- $db := ternary .Values.mysql .Values.externalDatabase .Values.mysql.enabled -}}
- name: WORDPRESS_DB_HOST
  value: "{{ include "wordpress.dbHost" . }}:3306"
- name: WORDPRESS_DB_NAME
  value: {{ $db.database | default "app_db" | quote }}
- name: WORDPRESS_DB_USER
  value: {{ $db.user | default "app_user" | quote }}
- name: WORDPRESS_DB_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ ternary (include "mysql.fullname" .) .Values.externalDatabase.existingSecret .Values.mysql.enabled }}
      key: mysql-password
{{- end }}
