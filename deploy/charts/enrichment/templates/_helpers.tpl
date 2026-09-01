{{- define "enrichment.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/part-of: vgkeep
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "enrichment.pgHost" -}}{{ .Chart.Name }}-pg{{- end }}
