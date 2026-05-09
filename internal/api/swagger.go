package api

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SESTelemetry API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/swagger/openapi.yaml",
      dom_id: "#swagger-ui",
      deepLinking: true,
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout"
    });
  </script>
</body>
</html>
`

const openAPISpecYAML = `openapi: 3.0.3
info:
  title: SESTelemetry API
  version: 1.0.0
  description: |
    API for dashboard graphics (timeseries) and current values for external resources.
servers:
  - url: http://localhost:8080
paths:
  /healthz:
    get:
      summary: Health check
      operationId: healthz
      responses:
        "200":
          description: Service is healthy
          content:
            application/json:
              schema:
                type: object
                properties:
                  status:
                    type: string
                    example: ok
  /api/v1/dashboard-config:
    get:
      summary: Dashboard metric configuration
      operationId: getDashboardConfig
      responses:
        "200":
          description: Dashboard cards and chart metric definitions
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/DashboardConfig"
  /api/v1/current:
    get:
      summary: Current metric values
      operationId: getCurrentValues
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
          description: Organization identifier
        - name: metric_keys
          in: query
          required: false
          schema:
            type: string
          description: Comma-separated list of metric keys
        - name: at
          in: query
          required: false
          schema:
            type: string
            format: date-time
          description: |
            Optional RFC3339 timestamp. When provided, returns the latest
            sample at or before this instant for each metric (snapshot
            lookup). When omitted, returns the most recent sample.
      responses:
        "200":
          description: Current values for requested metrics
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/CurrentResponse"
        "400":
          description: Missing required query parameter
          content:
            text/plain:
              schema:
                type: string
                example: organization_id is required
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
  /api/v1/dam-prices:
    get:
      summary: Day-Ahead Market hourly prices and volumes
      operationId: getDAMPrices
      description: |
        Returns hourly Day-Ahead Market (RDN) records collected from
        oree.com.ua. Records are available for delivery dates that the
        dam-collector has already fetched. Numeric fields may be omitted
        when the source XLS lacks a value for that hour.
      parameters:
        - name: zone
          in: query
          required: false
          schema:
            type: integer
            default: 2
            minimum: 1
            maximum: 99
          description: Trading zone (1 = Burshtyn island, 2 = unified UA grid)
        - name: from
          in: query
          required: false
          schema:
            type: string
            format: date
          description: Inclusive lower bound for delivery_date (YYYY-MM-DD). Defaults to today UTC.
        - name: to
          in: query
          required: false
          schema:
            type: string
            format: date
          description: Inclusive upper bound for delivery_date (YYYY-MM-DD). Defaults to from.
      responses:
        "200":
          description: DAM hourly rows for the requested zone and date range
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/DAMPricesResponse"
        "400":
          description: Invalid query parameters
          content:
            text/plain:
              schema:
                type: string
                example: from must be YYYY-MM-DD
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
  /api/v1/samples:
    get:
      summary: Raw telemetry_samples export (CSV)
      operationId: getSamples
      description: |
        Streams the raw per-poll rows from telemetry_samples that match
        the request as a CSV file (text/csv; charset=utf-8 with a
        leading UTF-8 BOM). Used by the dashboard's "Сирі дані" export
        mode when an analyst needs the un-bucketed sample stream
        rather than the aggregates from /api/v1/timeseries.

        The body is a header row time,metric_key,value,labels followed
        by one row per sample. labels is a JSON object when the sample
        carries label dimensions and empty otherwise. Rows are ordered
        by time ASC, metric_key ASC.

        Hard limits keep a misclick from streaming gigabytes:
          * range must be <= 31 days,
          * at most 20 metric_keys per request,
          * at most 1_000_000 rows per request (default 100_000).

        When the matched data exceeds limit the body ends with a
        sentinel row __TRUNCATED__,,<limit>,{...} so the dashboard
        can detect the partial export and warn the user. The HTTP
        Fetch API does not surface trailers, so signaling truncation
        in the body is the only channel that survives reliably.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
        - name: metric_keys
          in: query
          required: true
          schema:
            type: string
          description: Comma-separated list of metric keys (max 20)
        - name: from
          in: query
          required: true
          schema:
            type: string
            format: date-time
          description: Inclusive lower bound (RFC3339)
        - name: to
          in: query
          required: true
          schema:
            type: string
            format: date-time
          description: Exclusive upper bound (RFC3339); to-from must be ≤ 31 days
        - name: limit
          in: query
          required: false
          schema:
            type: integer
            minimum: 1
            maximum: 1000000
            default: 100000
          description: Maximum rows to return; the response is truncated when more data matches.
      responses:
        "200":
          description: CSV stream of telemetry_samples rows
          content:
            text/csv:
              schema:
                type: string
        "400":
          description: Invalid or missing query parameters
          content:
            text/plain:
              schema:
                type: string
                example: range must be <= 744h0m0s
  /api/v1/timeseries:
    get:
      summary: Timeseries for chart graphics
      operationId: getTimeseries
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
          description: Organization identifier
        - name: metric_key
          in: query
          required: false
          schema:
            type: string
          description: Single metric key
        - name: metric_keys
          in: query
          required: false
          schema:
            type: string
          description: Comma-separated list of metric keys
        - name: from
          in: query
          required: false
          schema:
            type: string
            format: date-time
          description: Start timestamp (RFC3339)
        - name: to
          in: query
          required: false
          schema:
            type: string
            format: date-time
          description: End timestamp (RFC3339)
        - name: bucket
          in: query
          required: false
          schema:
            type: string
            default: 15 minutes
          description: Aggregation bucket interval
        - name: tz
          in: query
          required: false
          schema:
            type: string
            default: UTC
          description: IANA timezone used to align bucket boundaries (e.g. Europe/Kyiv)
      responses:
        "200":
          description: Timeseries points for chart rendering
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/TimeseriesResponse"
        "400":
          description: Invalid or missing query parameters
          content:
            text/plain:
              schema:
                type: string
                example: metric_key or metric_keys is required
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
components:
  schemas:
    DashboardMetric:
      type: object
      properties:
        key:
          type: string
          example: active_pv_power_kw
        label:
          type: string
          example: Active PV Power
        unit:
          type: string
          example: kW
      required: [key, label, unit]
    DashboardConfig:
      type: object
      properties:
        cards:
          type: array
          items:
            $ref: "#/components/schemas/DashboardMetric"
        power_chart:
          type: array
          items:
            $ref: "#/components/schemas/DashboardMetric"
        energy_chart:
          type: array
          items:
            $ref: "#/components/schemas/DashboardMetric"
      required: [cards, power_chart, energy_chart]
    CurrentMetric:
      type: object
      properties:
        metric_key:
          type: string
          example: soc_percent
        value:
          type: number
          format: double
          example: 86.5
        time:
          type: string
          format: date-time
          example: "2026-04-29T18:00:00Z"
        labels:
          type: object
          additionalProperties:
            type: string
      required: [metric_key, value, time]
    CurrentResponse:
      type: object
      properties:
        organization_id:
          type: string
          example: demo-org
        metrics:
          type: object
          additionalProperties:
            $ref: "#/components/schemas/CurrentMetric"
      required: [organization_id, metrics]
    TimeseriesPoint:
      type: object
      properties:
        time:
          type: string
          format: date-time
          example: "2026-04-29T18:00:00Z"
        metric_key:
          type: string
          example: load_power_kw
        value:
          type: number
          format: double
          example: 12.45
      required: [time, metric_key, value]
    TimeseriesResponse:
      type: object
      properties:
        organization_id:
          type: string
          example: demo-org
        metric_keys:
          type: array
          items:
            type: string
          example: [active_pv_power_kw, load_power_kw]
        bucket:
          type: string
          example: 15 minutes
        from:
          type: string
          format: date-time
        to:
          type: string
          format: date-time
        points:
          type: array
          items:
            $ref: "#/components/schemas/TimeseriesPoint"
      required: [organization_id, metric_keys, bucket, from, to, points]
    DAMPrice:
      type: object
      properties:
        delivery_date:
          type: string
          format: date-time
          example: "2026-05-01T00:00:00Z"
        hour:
          type: integer
          minimum: 1
          maximum: 24
          example: 14
        zone:
          type: integer
          example: 2
        price_uah_per_mwh:
          type: number
          format: double
          nullable: true
          example: 5600.00
        sale_volume_mwh:
          type: number
          format: double
          nullable: true
          example: 3396.3
        purchase_volume_mwh:
          type: number
          format: double
          nullable: true
          example: 3396.3
        declared_sale_volume_mwh:
          type: number
          format: double
          nullable: true
        declared_purchase_volume_mwh:
          type: number
          format: double
          nullable: true
      required: [delivery_date, hour, zone]
    DAMPricesResponse:
      type: object
      properties:
        zone:
          type: integer
          example: 2
        from:
          type: string
          format: date-time
        to:
          type: string
          format: date-time
        prices:
          type: array
          items:
            $ref: "#/components/schemas/DAMPrice"
      required: [zone, from, to, prices]
`
