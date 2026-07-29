{{- define "bff.labels" -}}
app.kubernetes.io/name: bff
app.kubernetes.io/part-of: vgkeep
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "bff.valkeyHost" -}}bff-valkey{{- end }}
