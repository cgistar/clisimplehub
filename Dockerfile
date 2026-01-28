FROM --platform=$BUILDPLATFORM golang:1.21 AS builder

WORKDIR /src

COPY go.mod ./
RUN GOFLAGS=-mod=mod go mod download && go mod verify

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w" -o /out/cliSimpleHub-server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /data

COPY --from=builder /out/cliSimpleHub-server /app/cliSimpleHub-server

EXPOSE 5600

ENTRYPOINT ["/app/cliSimpleHub-server"]
