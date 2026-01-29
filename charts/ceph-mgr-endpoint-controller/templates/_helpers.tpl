{{- define "ceph-mgr-endpoint-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ceph-mgr-endpoint-controller.fullname" -}}
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

{{- define "ceph-mgr-endpoint-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ceph-mgr-endpoint-controller.labels" -}}
helm.sh/chart: {{ include "ceph-mgr-endpoint-controller.chart" . }}
{{ include "ceph-mgr-endpoint-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "ceph-mgr-endpoint-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ceph-mgr-endpoint-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "ceph-mgr-endpoint-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ceph-mgr-endpoint-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "ceph-mgr-endpoint-controller.imageTag" -}}
{{- .Values.image.tag }}
{{- end }}
