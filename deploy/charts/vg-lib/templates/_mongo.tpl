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
    # The metrics sidecar dials over pod-local loopback with full
    # verification; the serving cert must therefore name localhost.
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
  # Single replica: a voluntary drain blocks rather than silently
  # dropping the only copy. Inert on one node, correct shape on many.
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
        # mongod wants ONE combined PEM (cert + key), unlike postgres
        # and valkey; assemble it from the cert-manager secret and hand
        # it to the mongodb user (uid:gid 999:999 in the official
        # image). Secret mounts are read-only, hence the copy.
        - name: tls-perms
          image: busybox:1.37
          command: ["sh", "-c", "cat /tls-src/tls.crt /tls-src/tls.key > /tls/mongod.pem && cp /tls-src/ca.crt /tls/ca.crt && chmod 600 /tls/mongod.pem && chown -R 999:999 /tls"]
          volumeMounts:
            - { name: tls-src, mountPath: /tls-src, readOnly: true }
            - { name: tls, mountPath: /tls }
      containers:
        - name: mongo
          image: {{ .Values.mongo.image | quote }}
          # Dash-prefixed args: the image entrypoint prepends mongod.
          # The entrypoint's init phase (root user creation) detects
          # requireTLS and connects with TLS itself.
          args:
            - --tlsMode
            - requireTLS
            - --tlsCertificateKeyFile
            - /tls/mongod.pem
            # Server-side chain of trust mongod now mandates even for a
            # standalone node (SERVER-72839); the init container copies
            # the same CA the readiness probe already verifies against.
            - --tlsCAFile
            - /tls/ca.crt
            # Auth is SCRAM (root username/password below), not mutual
            # TLS: neither the app driver nor the probe ever presents a
            # client certificate, so the server must accept the
            # cert-less handshake or reject every caller outright.
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
              # Chain verification against the CA only; the probe skips
              # hostname checks (localhost is also in the cert SANs, so
              # the skip is not load-bearing).
              command: ["mongosh", "--tls", "--tlsCAFile", "/tls/ca.crt", "--tlsAllowInvalidHostnames", "--quiet", "--eval", "db.adminCommand('ping').ok"]
            periodSeconds: 5
            timeoutSeconds: 5
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
