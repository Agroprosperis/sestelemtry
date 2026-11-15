package main

import (
	"testing"

	"github.com/nesh/sestelemetry/internal/bootstrap"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/energyflow"
)

// An Encombi added to an organization feeds no energy flows and never
// becomes the host FusionSolar archive rows are stamped with; the
// SmartLogger roles stay what they were.
func TestEncombiDeviceKeepsSmartLoggerRoles(t *testing.T) {
	encombi := config.ModbusDevice{
		Modbus:          config.Modbus{Host: "10.28.40.103"},
		RegisterCatalog: "../../registers/encombi_ecpvx.yaml",
	}
	orgs := []config.Organization{
		{ID: "ke", ModbusDevices: []config.ModbusDevice{
			{Modbus: config.Modbus{Host: "10.24.40.238"}},
			{Modbus: config.Modbus{Host: "10.24.40.239"}, RegisterCatalog: encombi.RegisterCatalog},
		}},
		{ID: "ze", ModbusDevices: []config.ModbusDevice{
			encombi,
			{Modbus: config.Modbus{Host: "10.28.40.102"}, MetricKeys: []string{"total_energy_charged_kwh", "total_energy_discharged_kwh"}},
			{Modbus: config.Modbus{Host: "10.28.40.101"}, MetricKeys: []string{
				"accumulated_pv_energy_yield_kwh", "accumulated_electricity_purchased_kwh", "accumulated_electricity_sold_kwh",
			}},
		}},
	}
	catalogs, err := bootstrap.LoadDeviceCatalogs(&config.Root{Organizations: orgs})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]energyflow.Role{
		"10.24.40.238": energyflow.RoleSingle, "10.24.40.239": energyflow.RoleNone,
		"10.28.40.103": energyflow.RoleNone, "10.28.40.102": energyflow.RoleESS, "10.28.40.101": energyflow.RolePV,
	}
	for _, o := range toEnergyFlowOrgs(orgs, catalogs) {
		for _, d := range o.Devices {
			if energyflow.Role(d.Role) != want[d.Host] {
				t.Errorf("%s %s: role %q, want %q", o.ID, d.Host, d.Role, want[d.Host])
			}
		}
	}
	if got := archiveHost(orgs[1].ModbusDevices); got != "10.28.40.102" {
		t.Fatalf("archive host = %s, want the first SmartLogger", got)
	}
}
