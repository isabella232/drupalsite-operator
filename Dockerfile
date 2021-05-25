# Build the manager binary
FROM golang:1.15 as builder

WORKDIR /workspace

# 'config' directory for the controller to read the config file from
RUN mkdir config

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o manager main.go

# Use busybox image to copy required binaries
FROM busybox as binaries

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /usr/bin/openssl /bin/

# The operator requires binaries like wget, tar, rm, mkdir to download and organize the configuration files.
# Since distroless image doesn't have these, we are copying them from busybox
COPY --from=binaries /bin /bin
USER 65532:65532

ENTRYPOINT ["/manager"]
