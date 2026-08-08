FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /collector ./cmd/collector && \
    CGO_ENABLED=0 go build -o /api ./cmd/api && \
    CGO_ENABLED=0 go build -o /dam-collector ./cmd/dam-collector && \
    CGO_ENABLED=0 go build -o /weather-collector ./cmd/weather-collector && \
    CGO_ENABLED=0 go build -o /economics-recompute ./cmd/economics-recompute && \
    CGO_ENABLED=0 go build -o /alert-watchdog ./cmd/alert-watchdog

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /collector /usr/local/bin/collector
COPY --from=build /api /usr/local/bin/api
COPY --from=build /dam-collector /usr/local/bin/dam-collector
COPY --from=build /weather-collector /usr/local/bin/weather-collector
COPY --from=build /economics-recompute /usr/local/bin/economics-recompute
COPY --from=build /alert-watchdog /usr/local/bin/alert-watchdog
USER app
ENTRYPOINT ["/usr/local/bin/collector"]
