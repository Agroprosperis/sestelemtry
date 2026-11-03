package pvplan

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// orientationsSeed is the passport every known site starts from: the
// n8n flow's ses_pv_orientations_config with its duplicate rows
// dropped (the copies without coordinates are what put the n8n plan
// for ke, pe and pde on the equator). It is written only into a
// station that has no passport yet, so edits made in the UI survive
// every restart.
//
//go:embed orientations_seed.json
var orientationsSeed []byte

// SeedStations returns the starting passports, validated.
func SeedStations() ([]Station, error) {
	var out []Station
	if err := json.Unmarshal(orientationsSeed, &out); err != nil {
		return nil, fmt.Errorf("pvplan: decode seed: %w", err)
	}
	for _, st := range out {
		if err := st.Validate(); err != nil {
			return nil, fmt.Errorf("pvplan: seed %s: %w", st.OrganizationID, err)
		}
	}
	return out, nil
}
