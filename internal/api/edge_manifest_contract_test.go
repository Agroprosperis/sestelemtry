package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/edge"
	"github.com/nesh/sestelemetry/internal/storage"
)

// The manifest the cloud publishes must pass the edge's own validation
// with every type of manual block: the two sides are separate packages
// with their own structs, and a rejected manifest never reaches the
// loggers.
func TestDispatchManifestPassesEdgeValidation(t *testing.T) {
	env := dispatchTestEnv(t)
	m := dispatch.Model{Loads: make([]*float64, 24), Commands: make([]*dispatch.Command, 24), Cfg: env.defaults}
	cmds := []dispatch.Command{
		{Type: dispatch.CmdHold},
		{Type: dispatch.CmdFixed, Value: 100, Direction: "charge"},
		{Type: dispatch.CmdFixed, Value: 150, Direction: "discharge"},
		{Type: dispatch.CmdCover, Value: 300},
		{Type: dispatch.CmdSolar, Value: 200},
		{Type: dispatch.CmdTarget, Value: 80},
		{Type: dispatch.CmdCap, Value: 900},
		{Type: dispatch.CmdExport, Value: 300},
	}
	for i := range cmds {
		c := cmds[i]
		m.Commands[2*i] = &c // every other hour: the blocks stay apart
	}
	res, _ := dispatch.Simulate(env.inputs, m)
	doc, _ := buildDispatchManifest(env, m, res, storage.DispatchVersion{Version: 6, ConfirmedBy: "op@x", ConfirmedAt: env.now})
	doc.ManifestSeq = nextManifestSeq(nil, env.now)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	var em edge.Manifest
	if err := json.Unmarshal(raw, &em); err != nil {
		t.Fatal(err)
	}
	if err := em.ValidateForEdge("ze"); err != nil {
		t.Fatalf("the edge rejects the cloud's manifest: %v\n%s", err, raw)
	}
	if err := em.AcceptAfter(0, true); err != nil {
		t.Fatalf("not accepted while writing: %v", err)
	}
	if em.ManifestSeq == 0 || em.GridLimits.ExportCapKw != 850 || em.GridLimits.ImportLimitKw != 1700 {
		t.Fatalf("header: seq %d, grid limits %+v", em.ManifestSeq, em.GridLimits)
	}
	if len(em.ManualCommands) != len(cmds) {
		t.Fatalf("manual commands = %d, want %d: %+v", len(em.ManualCommands), len(cmds), em.ManualCommands)
	}
	for i, c := range cmds {
		at := env.start.Add(time.Duration(2*i)*time.Hour + 30*time.Minute)
		got := em.ManualAt(at)
		if got == nil || got.Type != string(c.Type) || got.Value != c.Value || got.Direction != c.Direction {
			t.Fatalf("hour %d: %+v, want %+v", 2*i, got, c)
		}
		if em.ManualAt(at.Add(time.Hour)) != nil {
			t.Fatalf("hour %d: a block where the desk has none", 2*i+1)
		}
	}
}
