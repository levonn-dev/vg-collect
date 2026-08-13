{{- define "bff.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/part-of: vgkeep
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "bff.valkeyHost" -}}{{ .Chart.Name }}-valkey{{- end }}
