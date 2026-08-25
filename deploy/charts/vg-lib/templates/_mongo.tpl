{{- define "vg-lib.mongo.certificate" -}}
{{- if .Values.mongo.enabled }}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ .Chart.Name }}-mongo-tls
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  secretName: {{ .Chart.Name }}-mongo-tls
  duration: 2160h
  dnsNames:
    - {{ .Chart.Name }}-mongo
    - {{ .Chart.Name }}-mongo.{{ .Release.Namespace }}.svc
    - {{ .Chart.Name }}-mongo.{{ .Release.Namespace }}.svc.cluster.local
    # The metrics sidecar dials over pod-local loopback with full verification; the cert must name localhost.
    - localhost
  issuerRef:
    name: vg-ca
    kind: ClusterIssuer
    group: cert-manager.io
{{- end }}
{{- end }}

{{- define "vg-lib.mongo.pdb" -}}
{{- if .Values.mongo.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ .Chart.Name }}-mongo
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  # Single replica: a voluntary drain blocks rather than dropping the only copy; a no-op until replicas > 1.
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-mongo
{{- end }}
{{- end }}

{{- define "vg-lib.mongo.service" -}}
{{- if .Values.mongo.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Chart.Name }}-mongo
  labels:
    app.kubernetes.io/name: {{ .Chart.Name }}-mongo
    app.kubernetes.io/part-of: vgkeep
spec:
  selector:
    app.kubernetes.io/name: {{ .Chart.Name }}-mongo
  ports:
    - { name: mongo, port: 27017, targetPort: mongo }
    - name: metrics
      port: 9216
      targetPort: metrics
{{- end }}
{{- end }}

{{- define "vg-lib.mongo.servicemonitor" -}}
{{- if .Values.mongo.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ .Chart.Name }}-mongo
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-mongo
  endpoints:
    - port: metrics
      interval: 30s
{{- end }}
{{- end }}

{{- define "vg-lib.mongo.statefulset" -}}
{{- if .Values.mongo.enabled }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{ .Chart.Name }}-mongo
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  serviceName: {{ .Chart.Name }}-mongo
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-mongo
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ .Chart.Name }}-mongo
        app.kubernetes.io/part-of: vgkeep
    spec:
      initContainers:
        # mongod wants ONE combined PEM (cert+key), unlike postgres/valkey; assembled here and chowned to
        # the mongodb user (999:999) since secret mounts are read-only.
        - name: tls-perms
          image: busybox:1.37
          command: ["sh", "-c", "cat /tls-src/tls.crt /tls-src/tls.key > /tls/mongod.pem && cp /tls-src/ca.crt /tls/ca.crt && chmod 600 /tls/mongod.pem && chown -R 999:999 /tls"]
          resources:
            requests: { cpu: 10m, memory: 16Mi }
            limits: { memory: 32Mi }
          volumeMounts:
            - { name: tls-src, mountPath: /tls-src, readOnly: true }
            - { name: tls, mountPath: /tls }
      containers:
        - name: mongo
          image: {{ .Values.mongo.image | quote }}
          # Dash-prefixed args: the entrypoint prepends mongod; its root-user init phase detects requireTLS
          # and connects with TLS itself.
          args:
            - --tlsMode
            - requireTLS
            - --tlsCertificateKeyFile
            - /tls/mongod.pem
            # mongod mandates server-side chain-of-trust even standalone (SERVER-72839); the init container
            # copies the same CA the readiness probe verifies against.
            - --tlsCAFile
            - /tls/ca.crt
            # Auth is SCRAM, not mutual TLS: neither the app driver nor the probe presents a client cert, so
            # the server must accept cert-less handshakes or reject every caller.
            - --tlsAllowConnectionsWithoutCertificates
            - --bind_ip_all
          env:
            - name: MONGO_INITDB_ROOT_USERNAME
              value: {{ .Values.mongo.username | quote }}
            - name: MONGO_INITDB_ROOT_PASSWORD
              valueFrom:
                secretKeyRef: { name: {{ .Chart.Name }}-secrets, key: mongo-password }
          ports:
            - { name: mongo, containerPort: 27017 }
          readinessProbe:
            exec:
              # mongosh with no host arg dials 127.0.0.1, but the cert's SANs are all dnsNames (no IP SAN), so
              # the hostname check would fail; --tlsAllowInvalidHostnames skips only that check, chain verification against the CA stays in force.
              command: ["mongosh", "--tls", "--tlsCAFile", "/tls/ca.crt", "--tlsAllowInvalidHostnames", "--quiet", "--eval", "db.adminCommand('ping').ok"]
            periodSeconds: 5
            timeoutSeconds: 5
          # Same check as readiness; wide thresholds so a slow query doesn't restart-loop the pod, only a wedged mongod trips this.
          livenessProbe:
            exec:
              command: ["mongosh", "--tls", "--tlsCAFile", "/tls/ca.crt", "--tlsAllowInvalidHostnames", "--quiet", "--eval", "db.adminCommand('ping').ok"]
            periodSeconds: 30
            timeoutSeconds: 5
            failureThreshold: 10
          resources: {{- toYaml .Values.mongo.resources | nindent 12 }}
          volumeMounts:
            - { name: tls, mountPath: /tls }
            - { name: data, mountPath: /data/db }
        - name: metrics
          image: {{ .Values.mongo.exporterImage | quote }}
          args:
            - --collect-all
            - --mongodb.direct-connect
          env:
            - name: MONGO_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{ .Chart.Name }}-secrets
                  key: mongo-password
            - name: MONGODB_URI
              value: "mongodb://{{ .Values.mongo.username }}:$(MONGO_PASSWORD)@localhost:27017/?tls=true&tlsCaFile=/metrics-ca/ca.crt"
          ports:
            - name: metrics
              containerPort: 9216
          resources: {{- toYaml .Values.mongo.exporterResources | nindent 12 }}
          volumeMounts:
            - name: metrics-ca
              mountPath: /metrics-ca
              readOnly: true
      volumes:
        - name: tls-src
          secret: { secretName: {{ .Chart.Name }}-mongo-tls }
        - name: tls
          emptyDir: {}
        - name: metrics-ca
          secret:
            secretName: {{ .Chart.Name }}-mongo-tls
            items:
              - key: ca.crt
                path: ca.crt
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: [ReadWriteOnce]
        resources:
          requests:
            storage: {{ .Values.mongo.storage }}
{{- end }}
{{- end }}

{{- define "vg-lib.mongo.ownerOnly" -}}
{{- if .Values.mongo.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ .Chart.Name }}-mongo-owner-only
  labels: {{- include "vg-lib.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: {{ .Chart.Name }}-mongo
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: {{ .Chart.Name }}
      ports:
        - { port: 27017, protocol: TCP }
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: vg-platform
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - protocol: TCP
          port: 9216
---
{{- end }}
{{- end }}
