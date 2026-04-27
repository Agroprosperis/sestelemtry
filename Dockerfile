FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /collector ./cmd/collector && \
    CGO_ENABLED=0 go build -o /api ./cmd/api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /collector /usr/local/bin/collector
COPY --from=build /api /usr/local/bin/api
ENTRYPOINT ["/usr/local/bin/collector"]
