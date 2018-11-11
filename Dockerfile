FROM registry.svc.ci.openshift.org/openshift/release:golang-1.10 AS builder
WORKDIR /go/src/github.com/openshift/csi-operator
COPY . .
RUN go build ./cmd/csi-operator

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
RUN useradd csi-operator
COPY --from=builder /go/src/github.com/openshift/csi-operator/csi-operator /usr/bin/
COPY deploy/openshift/image-references deploy/prerequisites/*.yaml /manifests/
COPY deploy/operator.yaml /manifests/99_operator.yaml
LABEL io.openshift.release.operator true
USER csi-operator
ENTRYPOINT ["/usr/bin/csi-operator"]
