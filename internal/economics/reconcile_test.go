package economics

import (
	"math"
	"strings"
	"testing"
)

// hourFlowPtrs builds n identical flow envelopes (pointers) for scaling.
func hourFlowPtrs(n int, f HourFlows) []*HourFlows {
	out := make([]*HourFlows, n)
	for i := range out {
		v := f
		out[i] = &v
	}
	return out
}

func sumPV(flows []*HourFlows) float64 {
	var s float64
	for _, f := range flows {
		if f != nil {
			s += f.PV
		}
	}
	return s
}

func TestReconcileNilCanonicalNoop(t *testing.T) {
	flows := hourFlowPtrs(3, HourFlows{PV: 10, GridImport: 5, EssCharged: 4, GridToEss: 4})
	res := reconcileFlows(flows, nil)
	if res.Applied {
		t.Fatal("Applied should be false without canonical")
	}
	if sumPV(flows) != 30 {
		t.Errorf("flows mutated: sumPV = %v, want 30", sumPV(flows))
	}
}

func TestReconcileScalesToCanonical(t *testing.T) {
	// Computed daily: PV=30, import=15, export=0, charged=12, discharged=6.
	flows := hourFlowPtrs(3, HourFlows{
		PV: 10, GridImport: 5, GridExport: 0,
		EssCharged: 4, EssDischarged: 2,
		PVToEss: 1, GridToEss: 3, EssToLoad: 2, EssToGrid: 0,
	})
	canonical := &CanonicalDaily{
		PV: 60, GridImport: 30, GridExport: 0,
		EssCharged: 24, EssDischarged: 12, Load: 0,
	}
	res := reconcileFlows(flows, canonical)
	if !res.Applied {
		t.Fatal("Applied should be true")
	}
	// Every base total should now equal its canonical target (factor 2x).
	var pv, imp, chg, dis, pvToEss, gridToEss, essToLoad float64
	for _, f := range flows {
		pv += f.PV
		imp += f.GridImport
		chg += f.EssCharged
		dis += f.EssDischarged
		pvToEss += f.PVToEss
		gridToEss += f.GridToEss
		essToLoad += f.EssToLoad
	}
	near(t, "pv", pv, 60)
	near(t, "import", imp, 30)
	near(t, "charged", chg, 24)
	near(t, "discharged", dis, 12)
	// Directional pairs scale with their parent counter.
	near(t, "pvToEss+gridToEss", pvToEss+gridToEss, chg)
	near(t, "essToLoad", essToLoad, 12) // essToLoad(2)*3 hours *2x = 12
	if f := res.Detail["pv"].Factor; math.Abs(f-2) > 1e-9 {
		t.Errorf("pv factor = %v, want 2", f)
	}
}

// Independent charge/PV factors must not manufacture solar charge: when
// fChg outruns fPV, the scaled PVToEss is clamped to the hour's scaled
// PV and the excess moves to GridToEss, keeping pvToEss+gridToEss equal
// to the scaled charge.
func TestReconcileKeepsPvToEssWithinPv(t *testing.T) {
	flows := hourFlowPtrs(1, HourFlows{
		PV: 10, GridImport: 4,
		EssCharged: 12, PVToEss: 8, GridToEss: 4,
	})
	canonical := &CanonicalDaily{PV: 10, GridImport: 4, EssCharged: 24}
	res := reconcileFlows(flows, canonical)
	if !res.Applied {
		t.Fatal("Applied should be true")
	}
	f := flows[0]
	near(t, "essCharged", f.EssCharged, 24)
	near(t, "pvToEss", f.PVToEss, 10)
	near(t, "gridToEss", f.GridToEss, 14)
	near(t, "pvToEss+gridToEss", f.PVToEss+f.GridToEss, f.EssCharged)
}

func TestReconcileZeroSumFlags(t *testing.T) {
	// Computed PV sum is zero -> cannot scale, must flag and not divide.
	flows := hourFlowPtrs(2, HourFlows{PV: 0, GridImport: 5, EssCharged: 0})
	canonical := &CanonicalDaily{PV: 100, GridImport: 10, EssCharged: 50}
	res := reconcileFlows(flows, canonical)
	if !res.Applied {
		t.Fatal("Applied should be true")
	}
	var hasNoScalePV, hasNoScaleChg bool
	for _, fl := range res.Flags {
		if fl == "no_scale:pv" {
			hasNoScalePV = true
		}
		if fl == "no_scale:ess_charged" {
			hasNoScaleChg = true
		}
	}
	if !hasNoScalePV || !hasNoScaleChg {
		t.Errorf("expected no_scale flags for pv and ess_charged, got %v", res.Flags)
	}
	for _, f := range flows {
		if math.IsNaN(f.PV) || math.IsInf(f.PV, 0) {
			t.Fatal("PV became NaN/Inf on zero-sum scale")
		}
	}
}

// TestReconcileRejectsImplausibleFactor pins the corrupted-canonical
// guard: a FusionSolar grid_import KPI wildly above the computed total
// must not scale the day's import — the metric stays at its computed
// value and a reconcile_rejected flag is raised.
func TestReconcileRejectsImplausibleFactor(t *testing.T) {
	// Computed import = 5*3 = 15; canonical 24958 => factor ~1664.
	flows := hourFlowPtrs(3, HourFlows{PV: 10, GridImport: 5})
	canonical := &CanonicalDaily{PV: 30, GridImport: 24958, Load: 0}
	res := reconcileFlows(flows, canonical)
	if !res.Applied {
		t.Fatal("Applied should be true")
	}
	var imp float64
	for _, f := range flows {
		imp += f.GridImport
	}
	if math.Abs(imp-15) > 1e-9 {
		t.Errorf("import should stay computed at 15, got %v", imp)
	}
	if f := res.Detail["grid_import"].Factor; math.Abs(f-1) > 1e-9 {
		t.Errorf("rejected grid_import factor should be 1, got %v", f)
	}
	var rejected bool
	for _, fl := range res.Flags {
		if strings.HasPrefix(fl, "reconcile_rejected:grid_import:") {
			rejected = true
		}
	}
	if !rejected {
		t.Errorf("expected reconcile_rejected flag for grid_import, got %v", res.Flags)
	}
	// PV factor (30/30 = 1) stays applied; sanity that valid metrics still scale.
	if f := res.Detail["pv"].Factor; math.Abs(f-1) > 1e-9 {
		t.Errorf("pv factor = %v, want 1", f)
	}
}

func TestReconcileLoadMismatchFlag(t *testing.T) {
	// Derived load = pv+import+dis-export-charged per hour.
	// One hour: pv=0,import=100,export=0,charged=0,discharged=0 -> load=100.
	flows := hourFlowPtrs(1, HourFlows{PV: 0, GridImport: 100})
	// Canonical use_power far from derived 100 -> mismatch flag.
	canonical := &CanonicalDaily{GridImport: 100, Load: 150}
	res := reconcileFlows(flows, canonical)
	var found bool
	for _, fl := range res.Flags {
		if strings.HasPrefix(fl, "load_mismatch:") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected load_mismatch flag, got %v", res.Flags)
	}
}

func TestReconcileLoadWithinToleranceNoFlag(t *testing.T) {
	flows := hourFlowPtrs(1, HourFlows{PV: 0, GridImport: 100})
	canonical := &CanonicalDaily{GridImport: 100, Load: 100.5} // 0.5% < 2%
	res := reconcileFlows(flows, canonical)
	for _, fl := range res.Flags {
		if strings.HasPrefix(fl, "load_mismatch:") {
			t.Errorf("unexpected load_mismatch within tolerance: %v", res.Flags)
		}
	}
}
