// Package fusionsolar imports historical 5-minute telemetry from the
// Huawei FusionSolar / SmartPVMS Northbound API and writes it into the
// same telemetry_samples hypertable the live Modbus collector feeds, so
// the dashboard / economics read paths consume the archive with no
// other code changes.
//
// The DB stores cumulative lifetime counters (accumulated_*,
// total_energy_*, soc_percent); both /api/v1/timeseries and
// /api/v1/energy-summary derive deltas and the on-the-fly UZE flow
// totals from these counters at read time. The importer therefore
// writes the FusionSolar SmartLogger cumulative fields verbatim at each
// 5-minute timestamp — no delta math on import — and every existing
// read path then works unchanged.
package fusionsolar

// Huawei device type ids relevant to the archive import. See the
// handoff doc in solar_fusion_archive/ for the full catalogue.
const (
	devTypeSmartLogger = 63 // Distributed SmartLogger: site PV/load/grid/ESS cumulative counters
	devTypeESS         = 41 // C&I / utility ESS: battery_soc + per-pack diagnostics
)

// Device is one FusionSolar device addressed by its Northbound devDn.
type Device struct {
	DevDn     string
	DevTypeID int
}

// PlantTopology pins which FusionSolar devices back one of our
// organizations. Most sites are a single SmartLogger that reports every
// site-level cumulative counter; Zhmerynskyi splits PV/grid/load onto
// one logger and ESS charge/discharge onto a second (EssLogger).
type PlantTopology struct {
	PlantCode string
	// Logger carries the PV / load / grid (and, on single-logger
	// sites, the ESS charge/discharge) cumulative counters.
	Logger Device
	// EssLogger is set only on dual-SmartLogger sites; when present it
	// is the source of total_charge / total_discharge instead of the
	// primary Logger.
	EssLogger *Device
	// EssDevices are the battery packs (devTypeId=41) used to derive
	// soc_percent (averaged across packs at each timestamp).
	EssDevices []Device
}

// Topology maps our organization id to the FusionSolar plant/device
// layout observed in the solar_fusion_archive probe. Adding a station
// is just another entry here — the importer and endpoint take the org
// id as a parameter.
var Topology = map[string]PlantTopology{
	// 2068.001 Ahrodar Bar
	"ab": {
		PlantCode: "NE=179121693",
		Logger:    Device{DevDn: "NE=179121695", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=181344806", DevTypeID: devTypeESS},
			{DevDn: "NE=181345297", DevTypeID: devTypeESS},
			{DevDn: "NE=181345052", DevTypeID: devTypeESS},
		},
	},
	// 2070.001 Krolevetskyi Elevator
	"ke": {
		PlantCode: "NE=182166719",
		Logger:    Device{DevDn: "NE=182166721", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=182451315", DevTypeID: devTypeESS},
			{DevDn: "NE=182449979", DevTypeID: devTypeESS},
			{DevDn: "NE=182449957", DevTypeID: devTypeESS},
		},
	},
	// 2114.001 Podillia Elevator
	"pde": {
		PlantCode: "NE=247346002",
		Logger:    Device{DevDn: "NE=247346004", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=251841094", DevTypeID: devTypeESS},
			{DevDn: "NE=251840849", DevTypeID: devTypeESS},
			{DevDn: "NE=251840603", DevTypeID: devTypeESS},
		},
	},
	// 2113.001 Duboviazivskyi Elevator
	"de": {
		PlantCode: "NE=246033234",
		Logger:    Device{DevDn: "NE=246033236", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=255860612", DevTypeID: devTypeESS},
			{DevDn: "NE=255860121", DevTypeID: devTypeESS},
			{DevDn: "NE=255860367", DevTypeID: devTypeESS},
		},
	},
	// 2115.001 Sorochansky Miroshnyk
	"sm": {
		PlantCode: "NE=247434102",
		Logger:    Device{DevDn: "NE=247434106", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=251818705", DevTypeID: devTypeESS},
			{DevDn: "NE=251822203", DevTypeID: devTypeESS},
			{DevDn: "NE=251818935", DevTypeID: devTypeESS},
		},
	},
	// 2095.001 Radivilivskiy Elevator
	"pe": {
		PlantCode: "NE=200471090",
		Logger:    Device{DevDn: "NE=200471092", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=217126231", DevTypeID: devTypeESS},
			{DevDn: "NE=217126476", DevTypeID: devTypeESS},
			{DevDn: "NE=217125985", DevTypeID: devTypeESS},
		},
	},
	// 2069.001 Zhmerynskyi Elevator — dual SmartLogger: site/grid/PV on
	// NE=182028533, ESS charge/discharge on NE=181361149.
	"ze": {
		PlantCode: "NE=181361147",
		Logger:    Device{DevDn: "NE=182028533", DevTypeID: devTypeSmartLogger},
		EssLogger: &Device{DevDn: "NE=181361149", DevTypeID: devTypeSmartLogger},
		EssDevices: []Device{
			{DevDn: "NE=182562531", DevTypeID: devTypeESS},
			{DevDn: "NE=182563022", DevTypeID: devTypeESS},
			{DevDn: "NE=182562777", DevTypeID: devTypeESS},
		},
	},
}
