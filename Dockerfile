FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /netra-api ./cmd/netra-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /netra-api /netra-api
ENV NETRA_DATA_DIR=/data NETRA_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/netra-api"]
