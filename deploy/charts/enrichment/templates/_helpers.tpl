{{- define "enrichment.labels" -}}
app.kubernetes.io/name: enrichment
app.kubernetes.io/part-of: vg-collect
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "enrichment.mongoHost" -}}enrichment-mongo{{- end }}

{{- define "enrichment.valkeyHost" -}}enrichment-valkey{{- end }}
