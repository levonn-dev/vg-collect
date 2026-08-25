{{- define "vg-lib.pdb" -}}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ .Chart.Name }}
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}
{{- end }}

{{- define "vg-lib.service" -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Chart.Name }}
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  selector:
    app.kubernetes.io/name: {{ .Chart.Name }}
  ports:
    - { name: http, port: 8080, targetPort: http }
{{- end }}

{{- define "vg-lib.serviceaccount" -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .Chart.Name }}
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
# None of these services call the Kubernetes API.
automountServiceAccountToken: false
{{- end }}
