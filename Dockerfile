FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.20-openshift-4.15 as builder
WORKDIR /go/src/github.com/openshift/coredns-ocp-dnsnameresolver
COPY . .

RUN git clone https://github.com/openshift/coredns /go/src/github.com/openshift/coredns
RUN GO111MODULE=on GOFLAGS=-mod=mod hack/add-plugin.sh /go/src/github.com/openshift/coredns

WORKDIR /go/src/github.com/openshift/coredns
RUN GO111MODULE=on GOFLAGS=-mod=vendor go build -o coredns .

FROM registry.ci.openshift.org/ocp/4.15:base
COPY --from=builder /go/src/github.com/openshift/coredns /usr/bin/

ENTRYPOINT ["/usr/bin/coredns"]

LABEL io.k8s.display-name="CoreDNS" \
      io.k8s.description="CoreDNS delivers the DNS and Discovery Service for a Kubernetes cluster." \
      maintainer="dev@lists.openshift.redhat.com"
