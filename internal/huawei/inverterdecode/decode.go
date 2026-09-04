// Package inverterdecode maps Huawei SUN2000 alarm/status codes (the
// remapped 51xxx hex values prod already shows) onto human labels.
//
// The lookup table inverter_alarm_decode.json is VENDORED from ems-spec
// docs/references/huawei/inverter_alarm_decode.json (commit ba17e44,
// schema 1.0) — the canon per the spec→prod workflow. Do not edit the
// JSON here or invent alarm names in code: fix the table in ems-spec
// first, then re-vendor.
//
// Formats (SmartLogger Issue 52, Table 2-7; encombi §3.1 offsets
// 12/14/16 for Major/Minor/Warning, offset 9 for status):
//
//	fault U32:  alarm_id = (v >> 16) & 0xFFFF, cause_id = v & 0xFFFF
//	status U16: same enum as SUN2000 register 32089 (+ SL 0xB000/0xC000)
//
// Raw hex stays the source of truth in every UI — decode is shown next
// to it, never instead of it.
package inverterdecode

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

//go:embed inverter_alarm_decode.json
var rawJSON []byte

// FaultDecode is one decoded Major/Minor/Warning U32.
type FaultDecode struct {
	AlarmID int
	CauseID int
	// NameUK/NameEN are empty when the alarm_id is not in the table.
	NameUK string
	NameEN string
	Level  string // major | minor | warning ("" when unknown)
	// CauseLabelUK is the decoded cause detail ("PV стрінг 3",
	// "Відсутність мережі або розімкнутий AC", "Cause ID 7"), empty
	// when there is nothing to say (cause_id = 0 with pattern none).
	CauseLabelUK string
	// SummaryUK is the display line: "2012 Зворотний струм стрінгу ·
	// PV стрінг 3". For unknown alarms: "Alarm ID 999 · Cause ID 7".
	SummaryUK string
	// Known is true when alarm_id was found in the table. Zero value
	// (v == 0) has Known=false and an empty SummaryUK.
	Known bool
}

// StatusDecode is one decoded status U16.
type StatusDecode struct {
	NameUK  string
	NameEN  string
	UIClass string // on_grid | starting | standby | fault | shutdown | unreachable | unknown
	// Known is false for codes missing from the table; NameUK then
	// carries the raw "0xHHHH" and UIClass is "unknown".
	Known bool
}

// --- table loading -------------------------------------------------

type tableAlarm struct {
	NameEN         string `json:"name_en"`
	NameUK         string `json:"name_uk"`
	Level          string `json:"level"`
	CauseIDPattern string `json:"cause_id_pattern"`
}

type tableStatus struct {
	NameEN  string `json:"name_en"`
	NameUK  string `json:"name_uk"`
	UIClass string `json:"ui_class"`
}

type causePattern struct {
	LabelEN    string `json:"label_en"`
	LabelUK    string `json:"label_uk"`
	FallbackEN string `json:"fallback_en"`
	FallbackUK string `json:"fallback_uk"`
	Values     map[string]struct {
		NameEN string `json:"name_en"`
		NameUK string `json:"name_uk"`
	} `json:"values"`
}

type decodeTable struct {
	SchemaVersion   string                  `json:"schema_version"`
	Alarms          map[string]tableAlarm   `json:"alarms"`
	StatusCodes     map[string]tableStatus  `json:"status_codes"`
	CauseIDPatterns map[string]causePattern `json:"cause_id_patterns"`
}

var table decodeTable

func init() {
	// The JSON is embedded at build time; a parse failure is a broken
	// build, not a runtime condition — fail loudly.
	if err := json.Unmarshal(rawJSON, &table); err != nil {
		panic(fmt.Sprintf("inverterdecode: embedded table: %v", err))
	}
	if len(table.Alarms) == 0 || len(table.StatusCodes) == 0 {
		panic("inverterdecode: embedded table is empty")
	}
}

// --- fault U32 -----------------------------------------------------

// DecodeFaultU32 decodes one Major/Minor/Warning register value.
func DecodeFaultU32(v uint32) FaultDecode {
	if v == 0 {
		return FaultDecode{}
	}
	d := FaultDecode{
		AlarmID: int(v >> 16 & 0xFFFF),
		CauseID: int(v & 0xFFFF),
	}
	a, ok := table.Alarms[strconv.Itoa(d.AlarmID)]
	if !ok {
		// Unknown alarm: numeric IDs only, no invented names.
		d.SummaryUK = fmt.Sprintf("Alarm ID %d", d.AlarmID)
		if d.CauseID > 0 {
			d.CauseLabelUK = fmt.Sprintf("Cause ID %d", d.CauseID)
			d.SummaryUK += " · " + d.CauseLabelUK
		}
		return d
	}
	d.Known = true
	d.NameUK = a.NameUK
	d.NameEN = a.NameEN
	d.Level = a.Level
	d.CauseLabelUK = causeLabelUK(a.CauseIDPattern, d.CauseID)
	d.SummaryUK = fmt.Sprintf("%d %s", d.AlarmID, a.NameUK)
	if d.CauseLabelUK != "" {
		d.SummaryUK += " · " + d.CauseLabelUK
	}
	return d
}

// ParseFault accepts the formats prod carries around: "0x7F00001",
// "0x7f00001" or a plain decimal like "131072001".
func ParseFault(s string) (FaultDecode, error) {
	v, err := parseU32(s)
	if err != nil {
		return FaultDecode{}, err
	}
	return DecodeFaultU32(v), nil
}

func causeLabelUK(pattern string, causeID int) string {
	p, ok := table.CauseIDPatterns[pattern]
	if !ok || pattern == "" || pattern == "none" {
		if causeID > 0 {
			return fmt.Sprintf("Cause ID %d", causeID)
		}
		return ""
	}
	if causeID == 0 {
		return ""
	}
	key := strconv.Itoa(causeID)
	if v, ok := p.Values[key]; ok {
		return v.NameUK
	}
	if p.LabelUK != "" {
		return strings.ReplaceAll(p.LabelUK, "{n}", key)
	}
	if p.FallbackUK != "" {
		return strings.ReplaceAll(p.FallbackUK, "{n}", key)
	}
	return fmt.Sprintf("Cause ID %d", causeID)
}

// --- status U16 ----------------------------------------------------

// DecodeStatusU16 decodes the remapped offset-9 status word.
func DecodeStatusU16(v uint16) StatusDecode {
	key := fmt.Sprintf("0x%04X", v)
	if st, ok := table.StatusCodes[key]; ok {
		return StatusDecode{NameUK: st.NameUK, NameEN: st.NameEN, UIClass: st.UIClass, Known: true}
	}
	return StatusDecode{NameUK: key, UIClass: "unknown"}
}

// ParseStatus mirrors ParseFault for U16 status values.
func ParseStatus(s string) (StatusDecode, error) {
	v, err := parseU32(s)
	if err != nil {
		return StatusDecode{}, err
	}
	if v > 0xFFFF {
		return StatusDecode{}, fmt.Errorf("inverterdecode: status %q overflows U16", s)
	}
	return DecodeStatusU16(uint16(v)), nil
}

func parseU32(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s, base = s[2:], 16
	}
	v, err := strconv.ParseUint(s, base, 32)
	if err != nil {
		return 0, fmt.Errorf("inverterdecode: parse %q: %w", s, err)
	}
	return uint32(v), nil
}
