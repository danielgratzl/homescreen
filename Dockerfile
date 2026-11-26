FROM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/homescreen .

FROM scratch
COPY --from=build /out/homescreen /homescreen
ENV CONFIG_PATH=/config/config.yaml PORT=8080
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/homescreen", "-healthcheck"]
ENTRYPOINT ["/homescreen"]
