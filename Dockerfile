# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/tokensaver ./cmd/tokensaver

FROM scratch
COPY --from=build /out/tokensaver /tokensaver
USER 65532:65532
ENV HOME=/tmp
EXPOSE 9231
ENTRYPOINT ["/tokensaver"]
CMD ["-config", "/etc/tokensaver/gateway.json"]
