{{- define "collection.labels" -}}
app.kubernetes.io/name: collection
app.kubernetes.io/part-of: vg-collect
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "collection.pgHost" -}}collection-pg{{- end }}

{{- define "collection.valkeyHost" -}}collection-valkey{{- end }}
