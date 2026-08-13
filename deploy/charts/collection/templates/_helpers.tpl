{{- define "collection.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/part-of: vgkeep
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "collection.pgHost" -}}{{ .Chart.Name }}-pg{{- end }}

{{- define "collection.valkeyHost" -}}{{ .Chart.Name }}-valkey{{- end }}
