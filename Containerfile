FROM registry.access.redhat.com/ubi10/go-toolset:1.25 AS builder

USER root

COPY . /go/src/image-mount-demo
WORKDIR /go/src/image-mount-demo
RUN dnf install -y gpgme-devel \
    && go build -tags "exclude_graphdriver_btrfs exclude_graphdriver_zfs"

FROM registry.access.redhat.com/ubi10/ubi:latest
COPY --from=builder /go/src/image-mount-demo/image-mount-demo /usr/local/bin/image-mount-demo
RUN dnf install -y containers-common gpgme \
    && mkdir -p /tmp/workdir \
    && chmod 1777 /tmp/workdir

# Set HOME to a writable location for OpenShift compatibility
# OpenShift runs containers with random UIDs, so /root won't be accessible
ENV HOME=/tmp/workdir

WORKDIR /tmp/workdir
ENTRYPOINT ["/usr/local/bin/image-mount-demo"]
CMD ["--help"]
