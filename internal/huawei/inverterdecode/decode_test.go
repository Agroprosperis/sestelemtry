package inverterdecode

import (
	"strings"
	"testing"
)

// Golden cases: prod_examples from the vendored table + the acceptance
// list (0x7DC0003, 0x7F00001, 0x0300, 0x0201, 0x0002).

func TestDecodeFaultStringBackfeedPVString3(t *testing.T) {
	d, err := ParseFault("0x7DC0003")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Known || d.AlarmID != 2012 || d.CauseID != 3 {
		t.Fatalf("decode = %+v, want known alarm 2012 cause 3", d)
	}
	if d.SummaryUK != "2012 Зворотний струм стрінгу · PV стрінг 3" {
		t.Fatalf("summary = %q", d.SummaryUK)
	}
	if d.Level != "warning" {
		t.Fatalf("level = %q, want warning", d.Level)
	}
}

func TestDecodeFaultGridLoss(t *testing.T) {
	for _, in := range []string{"0x7F00001", "0x7f00001", "133169153"} {
		d, err := ParseFault(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !d.Known || d.AlarmID != 2032 || d.CauseID != 1 || d.Level != "major" {
			t.Fatalf("%s: decode = %+v, want alarm 2032 cause 1 major", in, d)
		}
		if !strings.HasPrefix(d.SummaryUK, "2032 Втрата мережі · ") {
			t.Fatalf("%s: summary = %q", in, d.SummaryUK)
		}
		if d.CauseLabelUK != "Відсутність мережі або розімкнутий AC" {
			t.Fatalf("%s: cause = %q", in, d.CauseLabelUK)
		}
	}
}

func TestDecodeFaultZeroIsEmpty(t *testing.T) {
	if d := DecodeFaultU32(0); d.Known || d.SummaryUK != "" || d.AlarmID != 0 {
		t.Fatalf("zero decode = %+v, want empty", d)
	}
}

func TestDecodeFaultUnknownAlarmKeepsNumbers(t *testing.T) {
	// alarm_id 999 is not in the table; no invented names.
	d := DecodeFaultU32(999<<16 | 7)
	if d.Known || d.NameUK != "" {
		t.Fatalf("unknown alarm must not carry a name: %+v", d)
	}
	if d.SummaryUK != "Alarm ID 999 · Cause ID 7" {
		t.Fatalf("summary = %q", d.SummaryUK)
	}
}

func TestDecodeFaultGridLossUnknownCauseFallsBack(t *testing.T) {
	d := DecodeFaultU32(2032<<16 | 9)
	if d.CauseLabelUK != "Cause ID 9" {
		t.Fatalf("cause = %q, want fallback Cause ID 9", d.CauseLabelUK)
	}
}

func TestDecodeStatus(t *testing.T) {
	cases := []struct {
		in      string
		nameUK  string
		uiClass string
		known   bool
	}{
		{"0x0300", "OFF: неочікуване вимкнення", "fault", true},
		{"0x0201", "У мережі: обмеження потужності", "on_grid", true},
		{"0x0002", "Standby: детекція сонячного опромінення", "standby", true},
		{"0x0700", "Standby: немає опромінення", "standby", true},
		{"0xB000", "SmartLogger: перерва зв'язку", "unreachable", true},
		// 0xA000 (night standby seen live on ze) is not in the Huawei
		// enum table — raw hex + unknown, class stays per §6.2 logic.
		{"0xA000", "0xA000", "unknown", false},
	}
	for _, c := range cases {
		d, err := ParseStatus(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if d.NameUK != c.nameUK || d.UIClass != c.uiClass || d.Known != c.known {
			t.Fatalf("%s: decode = %+v, want %q/%q known=%v", c.in, d, c.nameUK, c.uiClass, c.known)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := ParseFault("not-a-number"); err == nil {
		t.Error("garbage fault must fail")
	}
	if _, err := ParseStatus("0x12345"); err == nil {
		t.Error("status overflowing U16 must fail")
	}
}
