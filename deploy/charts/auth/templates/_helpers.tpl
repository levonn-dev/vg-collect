{{- define "auth.labels" -}}
app.kubernetes.io/name: auth
app.kubernetes.io/part-of: vg-collect
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "auth.pgHost" -}}auth-pg{{- end }}
