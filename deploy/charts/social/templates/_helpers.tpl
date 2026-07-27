{{- define "social.labels" -}}
app.kubernetes.io/name: social
app.kubernetes.io/part-of: vg-collect
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "social.pgHost" -}}social-pg{{- end }}
