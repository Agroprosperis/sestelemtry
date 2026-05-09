package api

// RegisterMeta is the vendor-documented Modbus information attached
// to a metric_key — register address, payload data type, and the
// gain (multiplier the collector applies to the raw register value).
//
// The map is exposed via /api/v1/registers and embedded into both
// the raw-samples CSV and the dashboard's bucketed export so analysts
// can cross-reference values with the inverter datasheet without
// flipping between the CSV and registers/huawei_smartlogger.yaml.
type RegisterMeta struct {
	// Address is the vendor-documented Modbus holding register
	// address (FC3). For 32/64-bit values this is the first
	// register; widths follow from DataType.
	Address int `json:"address"`

	// DataType encodes payload width and signedness as documented in
	// the Huawei SmartLogger map. Mirrors registers.DataType but is
	// re-stated here as a plain string so the API package doesn't
	// pull in the collector's catalog code path.
	DataType string `json:"data_type"`

	// Gain is the multiplier the collector applies to the raw
	// register value to produce the engineering unit stored in
	// telemetry_samples. Embedding it in the export lets analysts
	// recover the raw counter (sample_value / gain) when they need
	// to compare against a SCADA snapshot.
	Gain float64 `json:"gain"`
}

// ModbusRegisterMetadata is the source of truth the API server uses
// for "what Modbus register backs this metric_key". The values are a
// hand-maintained mirror of registers/huawei_smartlogger.yaml; a unit
// test in registers_test.go loads that YAML and asserts every entry
// here matches so a vendor catalog change can't quietly drift away
// from the API.
//
// We keep a Go literal rather than wiring the API server to a
// filesystem catalog because:
//
//   - the API binary does not own the collector's register_catalog
//     config and shouldn't grow a hard runtime dependency on a YAML
//     path that may or may not exist on the API host;
//   - the catalog is small (~20 entries) so the duplication cost is
//     negligible; the drift test catches any divergence at PR time.
//
// Only metrics that come from telemetry_samples (i.e. inverter
// registers) are listed. Synthetic metrics (DAM price, PV forecast)
// have no Modbus address and are intentionally absent so the export
// formatter falls through to the un-annotated header for them.
var ModbusRegisterMetadata = map[string]RegisterMeta{
	"active_pv_power_kw":                    {Address: 40388, DataType: "UINT32", Gain: 0.001},
	"active_ess_power_kw":                   {Address: 40392, DataType: "INT32", Gain: 0.001},
	"power_supply_from_grid_day_kwh":        {Address: 40438, DataType: "UINT32", Gain: 0.01},
	"pv_energy_yield_day_kwh":               {Address: 40444, DataType: "UINT32", Gain: 0.01},
	"accumulated_pv_energy_yield_kwh":       {Address: 40446, DataType: "INT64", Gain: 0.01},
	"accumulated_electricity_purchased_kwh": {Address: 40450, DataType: "INT64", Gain: 0.01},
	"accumulated_electricity_sold_kwh":      {Address: 40454, DataType: "INT64", Gain: 0.01},
	"accumulated_power_consumption_kwh":     {Address: 40458, DataType: "INT64", Gain: 0.01},
	"total_power_supply_from_grid_kwh":      {Address: 40464, DataType: "INT64", Gain: 0.01},
	"energy_charged_day_kwh":                {Address: 40468, DataType: "UINT32", Gain: 0.01},
	"energy_discharged_day_kwh":             {Address: 40470, DataType: "UINT32", Gain: 0.01},
	"total_energy_charged_kwh":              {Address: 40472, DataType: "INT64", Gain: 0.01},
	"total_energy_discharged_kwh":           {Address: 40476, DataType: "UINT64", Gain: 0.01},
	"load_power_kw":                         {Address: 40503, DataType: "UINT32", Gain: 0.001},
	"grid_connected_active_power_kw":        {Address: 40505, DataType: "INT32", Gain: 0.001},
	"power_consumption_day_kwh":             {Address: 40509, DataType: "UINT32", Gain: 0.01},
	"electricity_sold_day_kwh":              {Address: 40511, DataType: "UINT32", Gain: 0.01},
	"electricity_purchased_day_kwh":         {Address: 40513, DataType: "UINT32", Gain: 0.01},
	"soc_percent":                           {Address: 40515, DataType: "UINT16", Gain: 0.1},
}

// RegistersResponse is the body of /api/v1/registers. It carries the
// map directly (rather than wrapping it in `registers: [{...}]`) so
// the dashboard can do an O(1) `metadata[metric_key]` lookup when
// formatting CSV headers.
type RegistersResponse struct {
	Metadata map[string]RegisterMeta `json:"metadata"`
}
