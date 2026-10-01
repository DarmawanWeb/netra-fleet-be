FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /netra-api ./cmd/netra-api && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /netra-api /netra-api
# The data volume must be writable by the nonroot user (uid 65532).
COPY --from=build --chown=65532:65532 /out/data /data
ENV NETRA_DATA_DIR=/data NETRA_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/netra-api"]
