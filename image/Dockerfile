FROM registry.access.redhat.com/ubi9-init

ARG ENVTEST_VERSION
ARG TARGETARCH=amd64

RUN dnf install -y tar gzip && dnf clean all

RUN curl -sL "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v${ENVTEST_VERSION}/envtest-v${ENVTEST_VERSION}-linux-${TARGETARCH}.tar.gz" \
    | tar xz -C /usr/local/bin --strip-components=2 controller-tools/envtest/

COPY etcd.service /etc/systemd/system/etcd.service
COPY kube-apiserver.service /etc/systemd/system/kube-apiserver.service

RUN systemctl enable etcd.service kube-apiserver.service

EXPOSE 6443
