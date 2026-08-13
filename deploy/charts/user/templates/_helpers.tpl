{{- define "user.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/part-of: vgkeep
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "user.pgHost" -}}{{ .Chart.Name }}-pg{{- end }}
