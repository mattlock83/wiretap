FROM golang:1.26.10-alpine AS build
ARG VERSION=dev
ARG COMMIT=unknown
ARG DIRTY=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X github.com/mattlock83/wiretap/internal/version.Version=${VERSION} -X github.com/mattlock83/wiretap/internal/version.Commit=${COMMIT} -X github.com/mattlock83/wiretap/internal/version.BuildDirty=${DIRTY}" \
    -o /out/wiretap ./cmd/wiretap \
    && mkdir -p /out/rootfs/tmp /out/rootfs/artifacts /out/rootfs/workspace \
    && chmod 1777 /out/rootfs/tmp \
    && chown -R 65532:65532 /out/rootfs/artifacts /out/rootfs/workspace

FROM scratch
COPY --from=build /out/rootfs/ /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/wiretap /wiretap
COPY LICENSE /LICENSE
ENV HOME=/tmp WIRETAP_OUTPUT_DIR=/artifacts WIRETAP_HTTP_LISTEN=0.0.0.0:18000 WIRETAP_PROXY_LISTEN=0.0.0.0:18080
WORKDIR /workspace
USER 65532:65532
EXPOSE 18000 18080
ENTRYPOINT ["/wiretap"]
