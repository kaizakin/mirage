# Build context is the repo root: docker build -f build/gateway.Dockerfile .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /gateway /gateway
# Numeric, so the kubelet can verify runAsNonRoot (65532 is distroless's nonroot user).
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
