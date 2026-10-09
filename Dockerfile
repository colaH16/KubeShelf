# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24.21.0-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=frontend /src/internal/webassets/dist ./internal/webassets/dist
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/kubeshelf ./cmd/kubeshelf

FROM alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0
RUN apk add --no-cache ca-certificates git openssh-client && addgroup -g 10001 kubeshelf && adduser -D -u 10001 -G kubeshelf kubeshelf && mkdir /work && chown 10001:10001 /work
COPY --from=backend /out/kubeshelf /usr/local/bin/kubeshelf
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubeshelf"]
