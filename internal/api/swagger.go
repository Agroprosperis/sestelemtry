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
`
