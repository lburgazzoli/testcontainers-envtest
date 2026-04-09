#!/bin/bash
set -e

# Start etcd in background on loopback
etcd \
  --listen-client-urls=http://127.0.0.1:2379 \
  --advertise-client-urls=http://127.0.0.1:2379 \
  --listen-peer-urls=http://127.0.0.1:2380 \
  --data-dir=/tmp/etcd-data &

ETCD_PID=$!

# Wait for etcd to be ready
for i in $(seq 1 30); do
  if etcdctl endpoint health --endpoints=http://127.0.0.1:2379 2>/dev/null; then
    break
  fi
  sleep 0.5
done

# Start kube-apiserver in foreground
exec kube-apiserver \
  --etcd-servers=http://127.0.0.1:2379 \
  --tls-cert-file=/etc/kubernetes/pki/apiserver.crt \
  --tls-private-key-file=/etc/kubernetes/pki/apiserver.key \
  --client-ca-file=/etc/kubernetes/pki/ca.crt \
  --service-account-signing-key-file=/etc/kubernetes/pki/sa.key \
  --service-account-key-file=/etc/kubernetes/pki/sa.pub \
  --service-account-issuer=https://kubernetes.default.svc.cluster.local \
  --authorization-mode=RBAC \
  --service-cluster-ip-range=10.0.0.0/24 \
  --disable-admission-plugins=ServiceAccount \
  "$@"
