FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /collector ./cmd/collector && \
    CGO_ENABLED=0 go build -o /api ./cmd/api && \
    CGO_ENABLED=0 go build -o /dam-collector ./cmd/dam-collector

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /collector /usr/local/bin/collector
COPY --from=build /api /usr/local/bin/api
COPY --from=build /dam-collector /usr/local/bin/dam-collector
USER app
ENTRYPOINT ["/usr/local/bin/collector"]
