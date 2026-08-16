{{- define "vg-lib.pg.certificate" -}}
{{- if .Values.postgres.enabled }}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ .Chart.Name }}-pg-tls
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  secretName: {{ .Chart.Name }}-pg-tls
  duration: 2160h
  dnsNames:
    - {{ .Chart.Name }}-pg
    - {{ .Chart.Name }}-pg.{{ .Release.Namespace }}.svc
    - {{ .Chart.Name }}-pg.{{ .Release.Namespace }}.svc.cluster.local
  issuerRef:
    name: vg-ca
    kind: ClusterIssuer
    group: cert-manager.io
{{- end }}
{{- end }}

{{- define "vg-lib.pg.pdb" -}}
{{- if .Values.postgres.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ .Chart.Name }}-pg
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  # Single replica: a voluntary drain blocks rather than silently
  # dropping the only copy. Inert on one node, correct shape on many.
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-pg
{{- end }}
{{- end }}

{{- define "vg-lib.pg.service" -}}
{{- if .Values.postgres.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Chart.Name }}-pg
  labels:
    app.kubernetes.io/name: {{ .Chart.Name }}-pg
    app.kubernetes.io/part-of: vgkeep
spec:
  selector:
    app.kubernetes.io/name: {{ .Chart.Name }}-pg
  ports:
    - { name: pg, port: 5432, targetPort: pg }
    - name: metrics
      port: 9187
      targetPort: metrics
{{- end }}
{{- end }}

{{- define "vg-lib.pg.servicemonitor" -}}
{{- if .Values.postgres.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ .Chart.Name }}-pg
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-pg
  endpoints:
    - port: metrics
      interval: 30s
{{- end }}
{{- end }}

{{- define "vg-lib.pg.statefulset" -}}
{{- if .Values.postgres.enabled }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{ .Chart.Name }}-pg
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  serviceName: {{ .Chart.Name }}-pg
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-pg
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ .Chart.Name }}-pg
        app.kubernetes.io/part-of: vgkeep
    spec:
      initContainers:
        # postgres requires the TLS key to be 0600 and owned by postgres
        # (uid 70 in alpine); secret mounts are read-only, so copy first.
        - name: tls-perms
          image: busybox:1.37
          command: ["sh", "-c", "cp /tls-src/* /tls/ && chmod 600 /tls/tls.key && chown 70:70 /tls/*"]
          volumeMounts:
            - { name: tls-src, mountPath: /tls-src, readOnly: true }
            - { name: tls, mountPath: /tls }
      containers:
        - name: postgres
          image: {{ .Values.postgres.image | quote }}
          args:
            - -c
            - ssl=on
            - -c
            - ssl_cert_file=/tls/tls.crt
            - -c
            - ssl_key_file=/tls/tls.key
          env:
            - name: POSTGRES_DB
              value: {{ .Values.postgres.database | quote }}
            - name: POSTGRES_USER
              value: {{ .Values.postgres.username | quote }}
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef: { name: {{ .Chart.Name }}-pg-credentials, key: password }
          ports:
            - { name: pg, containerPort: 5432 }
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", {{ .Values.postgres.username | quote }}]
            periodSeconds: 5
          resources: {{- toYaml .Values.postgres.resources | nindent 12 }}
          volumeMounts:
            - { name: tls, mountPath: /tls }
            - { name: data, mountPath: /var/lib/postgresql/data }
        - name: metrics
          image: {{ .Values.postgres.exporterImage | quote }}
          env:
            # Pod-local loopback: the stock image's pg_hba trusts local
            # connections, so no password crosses this hop; TLS guards
            # cross-pod traffic, and this traffic never leaves the pod.
            - name: DATA_SOURCE_NAME
              value: "postgresql://{{ .Values.postgres.username }}@localhost:5432/{{ .Values.postgres.database }}?sslmode=disable"
          ports:
            - name: metrics
              containerPort: 9187
      volumes:
        - name: tls-src
          secret: { secretName: {{ .Chart.Name }}-pg-tls }
        - name: tls
          emptyDir: {}
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: [ReadWriteOnce]
        resources:
          requests:
            storage: {{ .Values.postgres.storage }}
{{- end }}
{{- end }}

{{- define "vg-lib.pg.ownerOnly" -}}
{{- if .Values.postgres.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ .Chart.Name }}-pg-owner-only
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-pg
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: {{ .Chart.Name }}
      ports:
        - { port: 5432, protocol: TCP }
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: vg-platform
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - protocol: TCP
          port: 9187
---
{{- end }}
{{- end }}
