{{- define "user.labels" -}}
app.kubernetes.io/name: user
app.kubernetes.io/part-of: vgkeep
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "user.pgHost" -}}user-pg{{- end }}
