{{- define "vg-lib.valkey.certificate" -}}
{{- if .Values.valkey.enabled }}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ .Chart.Name }}-valkey-tls
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  secretName: {{ .Chart.Name }}-valkey-tls
  duration: 2160h
  dnsNames:
    - {{ .Chart.Name }}-valkey
    - {{ .Chart.Name }}-valkey.{{ .Release.Namespace }}.svc
    - {{ .Chart.Name }}-valkey.{{ .Release.Namespace }}.svc.cluster.local
    # The metrics sidecar dials over pod-local loopback with full verification; the cert must name localhost.
    - localhost
  issuerRef:
    name: vg-ca
    kind: ClusterIssuer
    group: cert-manager.io
{{- end }}
{{- end }}

{{- define "vg-lib.valkey.pdb" -}}
{{- if .Values.valkey.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ .Chart.Name }}-valkey
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  # Single replica: a voluntary drain blocks rather than dropping the only copy; a no-op until replicas > 1.
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-valkey
{{- end }}
{{- end }}

{{- define "vg-lib.valkey.service" -}}
{{- if .Values.valkey.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Chart.Name }}-valkey
  labels:
    app.kubernetes.io/name: {{ .Chart.Name }}-valkey
    app.kubernetes.io/part-of: vgkeep
spec:
  selector:
    app.kubernetes.io/name: {{ .Chart.Name }}-valkey
  ports:
    - { name: valkey, port: 6379, targetPort: valkey }
    - name: metrics
      port: 9121
      targetPort: metrics
{{- end }}
{{- end }}

{{- define "vg-lib.valkey.servicemonitor" -}}
{{- if .Values.valkey.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ .Chart.Name }}-valkey
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-valkey
  endpoints:
    - port: metrics
      interval: 30s
{{- end }}
{{- end }}

{{- define "vg-lib.valkey.statefulset" -}}
{{- if .Values.valkey.enabled }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{ .Chart.Name }}-valkey
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  serviceName: {{ .Chart.Name }}-valkey
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-valkey
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ .Chart.Name }}-valkey
        app.kubernetes.io/part-of: vgkeep
    spec:
      initContainers:
        # Secret mounts are read-only and root-owned; copied and chowned to the valkey user (999:1000 in the
        # alpine image). Valkey does not enforce key permissions like postgres; tight modes by choice.
        - name: tls-perms
          image: busybox:1.37
          command: ["sh", "-c", "cp /tls-src/* /tls/ && chmod 600 /tls/tls.key && chown 999:1000 /tls/*"]
          resources:
            requests: { cpu: 10m, memory: 16Mi }
            limits: { memory: 32Mi }
          volumeMounts:
            - { name: tls-src, mountPath: /tls-src, readOnly: true }
            - { name: tls, mountPath: /tls }
      containers:
        - name: valkey
          image: {{ .Values.valkey.image | quote }}
          args:
            # TLS-only listener; client cert auth off (the CA gates server identity, NetworkPolicy gates callers).
            - --tls-port
            - "6379"
            - --port
            - "0"
            - --tls-cert-file
            - /tls/tls.crt
            - --tls-key-file
            - /tls/tls.key
            - --tls-ca-cert-file
            - /tls/ca.crt
            - --tls-auth-clients
            - "no"
            - --save
            - ""
            - --appendonly
            - "no"
          ports:
            - { name: valkey, containerPort: 6379 }
          readinessProbe:
            exec:
              command: ["valkey-cli", "--tls", "--cacert", "/tls/ca.crt", "-p", "6379", "ping"]
            periodSeconds: 5
          # Same check as readiness; wide thresholds so a slow command doesn't restart-loop the pod, only a wedged valkey trips this.
          livenessProbe:
            exec:
              command: ["valkey-cli", "--tls", "--cacert", "/tls/ca.crt", "-p", "6379", "ping"]
            periodSeconds: 30
            failureThreshold: 10
          resources: {{- toYaml .Values.valkey.resources | nindent 12 }}
          volumeMounts:
            - { name: tls, mountPath: /tls }
            - { name: data, mountPath: /data }
        - name: metrics
          image: {{ .Values.valkey.exporterImage | quote }}
          env:
            - name: REDIS_ADDR
              value: rediss://localhost:6379
            - name: REDIS_EXPORTER_TLS_CA_CERT_FILE
              value: /metrics-ca/ca.crt
          ports:
            - name: metrics
              containerPort: 9121
          resources: {{- toYaml .Values.valkey.exporterResources | nindent 12 }}
          volumeMounts:
            - name: metrics-ca
              mountPath: /metrics-ca
              readOnly: true
      volumes:
        - name: tls-src
          secret: { secretName: {{ .Chart.Name }}-valkey-tls }
        - name: tls
          emptyDir: {}
        - name: data
          emptyDir: {}
        - name: metrics-ca
          secret:
            secretName: {{ .Chart.Name }}-valkey-tls
            items:
              - key: ca.crt
                path: ca.crt
{{- end }}
{{- end }}

{{- define "vg-lib.valkey.ownerOnly" -}}
{{- if .Values.valkey.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ .Chart.Name }}-valkey-owner-only
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-valkey
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: {{ .Chart.Name }}
      ports:
        - { port: 6379, protocol: TCP }
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: vg-platform
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - protocol: TCP
          port: 9121
---
{{- end }}
{{- end }}
