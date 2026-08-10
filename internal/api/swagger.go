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
  /api/v1/organizations:
    get:
      summary: Public organization metadata
      operationId: listOrganizations
      description: |
        Returns the list of configured organizations with their public
        metadata (id, display name, optional location). Sourced from
        the server YAML config; Modbus connection details are
        intentionally omitted. The dashboard uses this to populate the
        organization switcher and to look up coordinates for per-site
        features such as the weather widget.
      responses:
        "200":
          description: Organizations list
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/OrganizationsResponse"
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
  /api/v1/plant-inventory:
    get:
      summary: Latest plant passport / inventory snapshot
      operationId: getPlantInventory
      description: |
        Returns the newest rare Modbus plant-inventory snapshot for an
        organization (rated PV/ESS, ESS count, SOH, control mode). 404
        when the collector has not written a snapshot yet.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
      responses:
        "200":
          description: Latest inventory snapshot
          content:
            application/json:
              schema:
                type: object
        "400":
          description: Missing organization_id
        "404":
          description: No snapshot yet
  /api/v1/weather-forecast:
    get:
      summary: Cached Open-Meteo forecast for an organization
      operationId: getWeatherForecast
      description: |
        Returns the hourly + daily Open-Meteo forecast cached by the
        weather-collector service for the given organization. Empty
        hourly/daily arrays mean the collector hasn't populated this
        org / range yet — clients should fall back to fetching the
        forecast directly from Open-Meteo in that case.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
        - name: from
          in: query
          required: false
          schema:
            type: string
            format: date
          description: Inclusive lower bound (YYYY-MM-DD). Defaults to today UTC.
        - name: to
          in: query
          required: false
          schema:
            type: string
            format: date
          description: Inclusive upper bound (YYYY-MM-DD). Defaults to from+2d. Max span 31 days.
      responses:
        "200":
          description: Cached hourly + daily forecast rows
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/WeatherForecastResponse"
        "400":
          description: Invalid query parameters
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
  /api/v1/energy-flow-hourly:
    get:
      summary: Hourly directional energy flows (on-the-fly compute)
      operationId: getEnergyFlowHourly
      description: |
        Splits the requested calendar day (in the supplied timezone)
        into 24 one-hour windows and runs the same on-the-fly
        Recompute() that backs /api/v1/energy-summary on each window.
        Returns the four directional flow totals (pv_to_ess_kwh,
        grid_to_ess_kwh, ess_to_load_kwh, ess_to_grid_kwh) plus the
        per-hour ESS charge / discharge deltas needed to derive the
        hourly load via energy balance.

        The per-hour totals sum back to the daily totals served by
        /api/v1/energy-summary.flows for the same day, so the
        dashboard's economics view and the existing "Перетік за день"
        card stay arithmetically consistent.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
        - name: date
          in: query
          required: true
          schema:
            type: string
            format: date
          description: Calendar day (YYYY-MM-DD) interpreted in tz.
        - name: tz
          in: query
          required: false
          schema:
            type: string
            default: UTC
          description: |
            IANA timezone used to define the day boundaries
            (e.g. Europe/Kyiv). Unknown zones return 400.
      responses:
        "200":
          description: 24 hourly rows in the requested timezone
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/EnergyFlowHourlyResponse"
        "400":
          description: Invalid or missing query parameters
          content:
            text/plain:
              schema:
                type: string
                example: date must be YYYY-MM-DD
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
  /api/v1/uze-plan:
    get:
      summary: Recommended УЗЕ (BESS) dispatch for one day
      operationId: getUzePlan
      description: |
        Returns the hourly battery schedule an optimally-run system would
        have followed on the requested day, solved by the same
        dynamic-programming optimizer (project_net accounting) that backs
        the monthly "факт vs оптимум" reserve.

        This is a retrospective benchmark, not a forecast: the optimizer
        runs on the day's ACTUAL PV, load and РДН prices, so it answers
        "how should the battery have been run" rather than "what will
        happen". Hours without a РДН price are not tradable and the
        battery holds through them.

        recommended_ess_kw is signed like the telemetry metric
        active_ess_power_kw (positive = discharge, negative = charge) and,
        because buckets are hourly, equals the kWh moved in that hour.
        soc_pct is the modelled state of charge at the END of the hour on
        the pack's 10-90% operating band.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
        - name: date
          in: query
          required: true
          schema:
            type: string
            format: date
          description: Calendar day (YYYY-MM-DD) interpreted in tz.
        - name: tz
          in: query
          required: false
          schema:
            type: string
            default: UTC
          description: |
            IANA timezone used to define the day boundaries
            (e.g. Europe/Kyiv). Unknown zones return 400.
      responses:
        "200":
          description: |
            The day's recommended dispatch. available=false (with an empty
            hours array) when the day has no usable telemetry or the SOC
            window is degenerate.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/UzePlanResponse"
        "400":
          description: Invalid or missing query parameters
          content:
            text/plain:
              schema:
                type: string
                example: date must be YYYY-MM-DD
        "503":
          description: Economics service not configured
          content:
            text/plain:
              schema:
                type: string
                example: economics service not configured
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
  /api/v1/registers:
    get:
      summary: Modbus register metadata for export annotation
      operationId: getRegisters
      description: |
        Returns the metric_key → vendor-documented Modbus metadata
        (register address, data_type, gain) the dashboard uses to
        annotate CSV headers in bucketed exports. Static map; the
        body changes only when the upstream registers/*.yaml catalog
        is updated and the API map is re-synced.
      responses:
        "200":
          description: metric_key → RegisterMeta map
          content:
            application/json:
              schema:
                type: object
                properties:
                  metadata:
                    type: object
                    additionalProperties:
                      type: object
                      properties:
                        address:
                          type: integer
                          example: 40388
                        data_type:
                          type: string
                          example: UINT32
                        gain:
                          type: number
                          format: double
                          example: 0.001
                      required: [address, data_type, gain]
                required: [metadata]
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

        The body is a header row
        time,metric_key,modbus_register,data_type,gain,value,labels
        followed by one row per sample. modbus_register/data_type/
        gain replicate the vendor metadata for the metric_key (empty
        when the metric isn't backed by a Modbus register). labels is
        a JSON object when the sample carries label dimensions and
        empty otherwise. Rows are ordered by metric_key ASC, time ASC
        (metric-major): this streams straight off the
        (organization_id, metric_key, time) index without a global
        sort, so large multi-week pulls don't time out. Consumers that
        need a time-major view should sort client-side.

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
        - name: tz
          in: query
          required: false
          schema:
            type: string
            default: UTC
          description: |
            IANA timezone name used to render the CSV "time" column
            (e.g. Europe/Kyiv -> +03:00 offset). Defaults to UTC.
            Unknown zone names produce a 400 -- typos must not silently
            fall back to UTC and mask phantom drift.
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
  /api/v1/organization-tariffs:
    get:
      summary: Persisted economics tariff bundle for an organization
      operationId: getOrganizationTariffs
      description: |
        Returns the per-org tariff settings the economics dashboard
        uses to compute baseline / actual cost columns. Returns 404
        when the org has never saved a tariff bundle — the frontend
        treats that as "use bundled defaults" and only persists when
        the analyst edits the form.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
      responses:
        "200":
          description: Persisted tariff bundle
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/OrgTariffs"
        "400":
          description: Missing organization_id
          content:
            text/plain:
              schema:
                type: string
                example: organization_id is required
        "404":
          description: No tariffs saved for this organization
          content:
            text/plain:
              schema:
                type: string
                example: tariffs not found
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
    put:
      summary: Save the economics tariff bundle for an organization
      operationId: putOrganizationTariffs
      description: |
        Replaces the persisted tariff bundle for the given org
        (last-writer-wins). All numeric fields must be finite;
        export_discount and vat_rate are 0..1 fractions;
        ess_capacity_kwh must be > 0; everything else must be >= 0.
        Unknown JSON fields are rejected so a frontend out of sync
        with the API DTO fails loudly rather than silently dropping
        data.
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/OrgTariffs"
      responses:
        "204":
          description: Tariffs saved
        "400":
          description: Missing organization_id, malformed body, or out-of-range field
          content:
            text/plain:
              schema:
                type: string
                example: vat_rate must be in [0, 1]
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
  /api/v1/alert-settings:
    get:
      summary: Site-wide alert delivery settings
      operationId: getAlertSettings
      description: |
        Returns the SMTP server, thresholds and default recipient list
        used by alert-watchdog. The SMTP password is never included;
        smtp_password_configured only reports whether one is stored.
        When nothing has been saved, the payload is the "alerts:" block
        from config.yaml (saved=false) so the settings page shows the
        behaviour currently in force instead of blanks.
      responses:
        "200":
          description: Alert settings
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/AlertSettings"
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
    put:
      summary: Save the site-wide alert delivery settings
      operationId: putAlertSettings
      description: |
        Replaces the settings (last-writer-wins). Durations are Go
        duration strings ("10m"). repeat_interval 0 disables reminders.
        smtp_password is optional: omitted keeps the stored password,
        "" clears it, any other value replaces it. Unknown JSON fields
        are rejected.
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/AlertSettingsUpdate"
      responses:
        "204":
          description: Settings saved
        "400":
          description: Malformed body or invalid value
          content:
            text/plain:
              schema:
                type: string
                example: stale_after (5m0s) must be >= check_interval (30m0s)
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
  /api/v1/alert-settings/test-email:
    post:
      summary: Send a test alert email
      operationId: postAlertTestEmail
      description: |
        Delivers a test message to the addresses that would receive a
        real alert, ignoring the enabled switches. With organization_id
        the organization's effective list is used; without it, the
        default one.
      parameters:
        - name: organization_id
          in: query
          required: false
          schema:
            type: string
      responses:
        "200":
          description: Message accepted by the relay
          content:
            application/json:
              schema:
                type: object
                properties:
                  recipients:
                    type: array
                    items:
                      type: string
                required: [recipients]
        "400":
          description: No recipients configured or unusable SMTP settings
          content:
            text/plain:
              schema:
                type: string
                example: no recipients configured
        "502":
          description: The mail relay rejected the message
          content:
            text/plain:
              schema:
                type: string
                example: "alerts: smtp auth: 535 5.7.8 authentication failed"
  /api/v1/organization-alert-settings:
    get:
      summary: Per-organization alert overrides
      operationId: getOrganizationAlertSettings
      description: |
        Overrides keyed by organization id. Organizations the operator
        never customized are absent: they are enabled and inherit the
        default recipient list.
      responses:
        "200":
          description: Overrides by organization
          content:
            application/json:
              schema:
                type: object
                properties:
                  organizations:
                    type: object
                    additionalProperties:
                      $ref: "#/components/schemas/OrgAlertSettings"
                required: [organizations]
        "500":
          description: Internal server error
          content:
            text/plain:
              schema:
                type: string
                example: internal server error
    put:
      summary: Save one organization's alert override
      operationId: putOrganizationAlertSettings
      description: |
        A non-empty recipients list replaces the default one for this
        organization (it is not merged). An empty list means "inherit
        the default list".
      parameters:
        - name: organization_id
          in: query
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/OrgAlertSettings"
      responses:
        "204":
          description: Override saved
        "400":
          description: Missing organization_id, malformed body, or invalid address
          content:
            text/plain:
              schema:
                type: string
                example: 'recipient is not a valid address: "ops at example.com"'
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
    LocationInfo:
      type: object
      properties:
        latitude:
          type: number
          format: double
          example: 49.0191004
        longitude:
          type: number
          format: double
          example: 28.1260144
        city:
          type: string
          example: Жмеринка
      required: [latitude, longitude, city]
    OrganizationInfo:
      type: object
      properties:
        id:
          type: string
          example: ze
        name:
          type: string
          example: ZE
        location:
          $ref: "#/components/schemas/LocationInfo"
      required: [id]
    OrganizationsResponse:
      type: object
      properties:
        organizations:
          type: array
          items:
            $ref: "#/components/schemas/OrganizationInfo"
      required: [organizations]
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
    WeatherForecastHour:
      type: object
      properties:
        hour:
          type: string
          format: date-time
        temperature_2m_c:
          type: number
          format: double
          nullable: true
        cloud_cover_pct:
          type: number
          format: double
          nullable: true
        is_day:
          type: boolean
          nullable: true
        shortwave_wm2:
          type: number
          format: double
          nullable: true
        direct_wm2:
          type: number
          format: double
          nullable: true
        diffuse_wm2:
          type: number
          format: double
          nullable: true
        gti_instant_wm2:
          type: number
          format: double
          nullable: true
        fetched_at:
          type: string
          format: date-time
      required: [hour, fetched_at]
    WeatherForecastDay:
      type: object
      properties:
        day:
          type: string
          format: date
        sunrise:
          type: string
          format: date-time
          nullable: true
        sunset:
          type: string
          format: date-time
          nullable: true
        daylight_duration_s:
          type: number
          format: double
          nullable: true
        sunshine_duration_s:
          type: number
          format: double
          nullable: true
        shortwave_radiation_sum:
          type: number
          format: double
          nullable: true
        fetched_at:
          type: string
          format: date-time
      required: [day, fetched_at]
    WeatherForecastResponse:
      type: object
      properties:
        organization_id:
          type: string
        from:
          type: string
          format: date-time
        to:
          type: string
          format: date-time
        hourly:
          type: array
          items:
            $ref: "#/components/schemas/WeatherForecastHour"
        daily:
          type: array
          items:
            $ref: "#/components/schemas/WeatherForecastDay"
      required: [organization_id, from, to, hourly, daily]
    EnergyFlowHourlyRow:
      type: object
      properties:
        hour:
          type: integer
          minimum: 0
          maximum: 23
          example: 14
        from:
          type: string
          format: date-time
        to:
          type: string
          format: date-time
        pv_to_ess_kwh:
          type: number
          format: double
        grid_to_ess_kwh:
          type: number
          format: double
        ess_to_load_kwh:
          type: number
          format: double
        ess_to_grid_kwh:
          type: number
          format: double
        ess_charged_kwh:
          type: number
          format: double
        ess_discharged_kwh:
          type: number
          format: double
        skipped_intervals:
          type: integer
          example: 0
        warnings:
          type: array
          items:
            type: string
      required:
        - hour
        - from
        - to
        - pv_to_ess_kwh
        - grid_to_ess_kwh
        - ess_to_load_kwh
        - ess_to_grid_kwh
        - ess_charged_kwh
        - ess_discharged_kwh
        - skipped_intervals
    EnergyFlowHourlyResponse:
      type: object
      properties:
        organization_id:
          type: string
          example: ze
        date:
          type: string
          format: date
          example: "2026-05-09"
        tz:
          type: string
          example: Europe/Kyiv
        hours:
          type: array
          minItems: 24
          maxItems: 24
          items:
            $ref: "#/components/schemas/EnergyFlowHourlyRow"
      required: [organization_id, date, tz, hours]
    UzePlanHour:
      type: object
      properties:
        hour:
          type: integer
          minimum: 0
          maximum: 23
        recommended_ess_kw:
          type: number
          format: double
          description: >
            Average ESS power over the hour, signed like
            active_ess_power_kw: positive = discharge, negative = charge.
          example: -180.5
        soc_pct:
          type: number
          format: double
          minimum: 0
          maximum: 100
          description: Modelled state of charge at the END of the hour.
        ess_to_load_kwh:
          type: number
          format: double
        ess_to_grid_kwh:
          type: number
          format: double
        pv_to_ess_kwh:
          type: number
          format: double
        grid_to_ess_kwh:
          type: number
          format: double
        effect_uah:
          type: number
          format: double
          description: >
            This hour's contribution to the day's optimum: discharge value
            less grid-charge cost, less the export forgone by storing PV,
            less degradation.
        action:
          type: string
          enum: [charge, discharge, hold]
        reason_code:
          type: string
          enum:
            - DISCHARGE_PEAK_IMPORT
            - DISCHARGE_TO_GRID
            - CHARGE_PV_SURPLUS
            - CHARGE_GRID_CHEAP
            - HOLD_FOR_FUTURE_PEAK
            - HOLD_LOW_PRICE
            - NO_PRICE
        reason_text:
          type: string
          description: Operator-facing explanation (Ukrainian).
        rdn_uah_per_kwh:
          type: number
          format: double
          nullable: true
      required: [hour, recommended_ess_kw, soc_pct, action, reason_code]
    UzePlanTotals:
      type: object
      description: |
        The day's optimum-vs-fact headline plus the waterfall legs behind
        the optimum. reserve_uah = max(0, optimum - fact);
        captured_share = fact / optimum (may be negative when the
        realised dispatch lost money).
      properties:
        optimum_uah:
          type: number
          format: double
        fact_uah:
          type: number
          format: double
        reserve_uah:
          type: number
          format: double
        captured_share:
          type: number
          format: double
        charge_pv_kwh:
          type: number
          format: double
        charge_grid_kwh:
          type: number
          format: double
        discharge_kwh:
          type: number
          format: double
        export_val_uah:
          type: number
          format: double
        load_val_uah:
          type: number
          format: double
        charge_pv_cost_uah:
          type: number
          format: double
        grid_cost_uah:
          type: number
          format: double
        degradation_uah:
          type: number
          format: double
    UzePlanResponse:
      type: object
      properties:
        organization_id:
          type: string
          example: ze
        date:
          type: string
          format: date
          example: "2026-05-09"
        tz:
          type: string
          example: Europe/Kyiv
        available:
          type: boolean
          description: >
            False when the day has no usable telemetry or the SOC window
            is degenerate; hours is then empty.
        soc_start_pct:
          type: number
          format: double
          description: Modelled state of charge before hour 0.
        capacity_kwh:
          type: number
          format: double
          description: Usable ESS capacity (the 10-90% SOC window).
        power_kw:
          type: number
          format: double
        hours:
          type: array
          maxItems: 24
          items:
            $ref: "#/components/schemas/UzePlanHour"
        totals:
          $ref: "#/components/schemas/UzePlanTotals"
        warnings:
          type: array
          description: >
            Data-quality flags, e.g. NO_RDN, PARTIAL_RDN:3, NO_SOC,
            TELEMETRY_ANOMALY:2.
          items:
            type: string
      required: [organization_id, date, tz, available, hours, totals]
    OrgTariffs:
      type: object
      description: |
        Per-organization tariff settings persisted by the economics
        dashboard. Numeric fields are UAH/kWh except export_discount
        and vat_rate (unitless 0..1 fractions). Field shape mirrors
        the React Tariffs type (snake_case on the wire).
      properties:
        distribution_uah_per_kwh:
          type: number
          format: double
          minimum: 0
        transmission_uah_per_kwh:
          type: number
          format: double
          minimum: 0
        supplier_margin_uah_per_kwh:
          type: number
          format: double
          description: >
            Supplier per-kWh margin added to the import price (used when
            supplier_margin_mode is "abs"). May be negative (discount).
        supplier_margin_mode:
          type: string
          enum: ["abs", "pct"]
          description: >
            How the supplier margin is applied: "abs" (flat UAH/kWh, the
            default) or "pct" (percent of the RDN market price).
        supplier_margin_pct:
          type: number
          format: double
          description: >
            Supplier margin as a percent of the RDN price (used when
            supplier_margin_mode is "pct"). May be negative (discount).
        other_fees_uah_per_kwh:
          type: number
          format: double
          minimum: 0
        export_discount:
          type: number
          format: double
          minimum: 0
          maximum: 1
        degradation_uah_per_kwh:
          type: number
          format: double
          minimum: 0
        include_vat:
          type: boolean
        vat_rate:
          type: number
          format: double
          minimum: 0
          maximum: 1
        ess_capacity_kwh:
          type: number
          format: double
          exclusiveMinimum: 0
        ess_power_limit_kw:
          type: number
          format: double
          minimum: 0
          description: >
            Per-object nominal charge/discharge power ceiling (kW) for the
            УЗЕ anomaly filter. 0 (or omitted) derives a ~1C fallback.
        roundtrip_efficiency:
          type: number
          format: double
          minimum: 0
          maximum: 1
          description: >
            Per-object ESS round-trip efficiency (0..1). 0 (or omitted)
            keeps the empirical throughput-derived estimate.
        capex_uah:
          type: number
          format: double
          minimum: 0
          description: >
            One-time project capital expenditure (UAH). Display-only
            metadata for the payback/ROI panel; 0 (or omitted) hides it.
        planned_payback_months:
          type: number
          format: double
          minimum: 0
          description: >
            Business-plan payback period (months from the start of
            operation). Display-only metadata for the payback page's
            plan-vs-forecast comparison; 0 (or omitted) hides it.
      required:
        - distribution_uah_per_kwh
        - transmission_uah_per_kwh
        - supplier_margin_uah_per_kwh
        - other_fees_uah_per_kwh
        - export_discount
        - degradation_uah_per_kwh
        - include_vat
        - vat_rate
        - ess_capacity_kwh
    SMTPSettings:
      type: object
      description: Mail server, without the password.
      properties:
        host:
          type: string
          example: smtp.gmail.com
        port:
          type: integer
          example: 587
        tls:
          type: string
          enum: [starttls, implicit, none]
        username:
          type: string
        from:
          type: string
          example: СЕС Моніторинг <alerts@example.com>
        timeout:
          type: string
          example: 20s
      required: [host, port, tls, username, from, timeout]
    AlertSettingsBase:
      type: object
      properties:
        enabled:
          type: boolean
        check_interval:
          type: string
          description: How often freshness is sampled (10s..1h).
          example: 1m
        stale_after:
          type: string
          description: Silence that counts as a lost connection (1m..24h, >= check_interval).
          example: 10m
        repeat_interval:
          type: string
          description: Reminder cadence for an ongoing outage; "0s" disables reminders.
          example: 6h
        notify_recovery:
          type: boolean
        smtp:
          $ref: "#/components/schemas/SMTPSettings"
        recipients:
          type: array
          description: Default address list, inherited by organizations without their own.
          items:
            type: string
      required:
        - enabled
        - check_interval
        - stale_after
        - repeat_interval
        - notify_recovery
        - smtp
        - recipients
    AlertSettings:
      allOf:
        - $ref: "#/components/schemas/AlertSettingsBase"
        - type: object
          properties:
            smtp_password_configured:
              type: boolean
              description: Whether a password is stored. The password itself is never returned.
            saved:
              type: boolean
              description: False when the payload is the config.yaml fallback.
          required: [smtp_password_configured, saved]
    AlertSettingsUpdate:
      allOf:
        - $ref: "#/components/schemas/AlertSettingsBase"
        - type: object
          properties:
            smtp_password:
              type: string
              nullable: true
              description: >
                Omitted or null keeps the stored password, "" clears it,
                any other value replaces it.
    OrgAlertSettings:
      type: object
      properties:
        enabled:
          type: boolean
        recipients:
          type: array
          description: Replaces the default list for this organization; empty inherits it.
          items:
            type: string
      required: [enabled, recipients]
`
