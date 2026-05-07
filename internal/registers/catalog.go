package registers

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SwapType is byte/word order hint; v1 supports ABCD_BE only (big-endian over register stream).
type SwapType string

const SwapABCD_BE SwapType = "ABCD_BE"

type DataType string

const (
	DTUint16 DataType = "UINT16"
	DTUint32 DataType = "UINT32"
	DTInt32  DataType = "INT32"
	DTInt64  DataType = "INT64"
	DTUint64 DataType = "UINT64"
)

// Entry is one logical metric in the catalog.
type Entry struct {
	MetricKey string   `yaml:"metric_key"`
	Name      string   `yaml:"name"`
	Address   int      `yaml:"address"` // vendor-documented address
	DataType  DataType `yaml:"data_type"`
	SwapType  SwapType `yaml:"swap_type"`
	Gain      float64  `yaml:"gain"`
	Offset    float64  `yaml:"offset"`
}

// Catalog is the full register map plus addressing defaults.
type Catalog struct {
	Addressing struct {
		HoldingAddressBase int `yaml:"holding_address_base"`
	} `yaml:"addressing"`

	Registers []Entry `yaml:"registers"`
}

// ResolvedEntry has computed PDU start and register width.
type ResolvedEntry struct {
	Entry
	PDUStart    uint16
	WordCount   uint16
	PDUEnd      uint16 // inclusive last register index
}

func (e Entry) WordCountForType() (uint16, error) {
	switch e.DataType {
	case DTUint16:
		return 1, nil
	case DTUint32, DTInt32:
		return 2, nil
	case DTInt64, DTUint64:
		return 4, nil
	default:
		return 0, fmt.Errorf("registers: unknown data_type %q for %q", e.DataType, e.MetricKey)
	}
}

// Resolve applies holding_address_base to compute zero-based PDU addresses for FC3/FC4.
func (c *Catalog) Resolve(holdingBase int) ([]ResolvedEntry, error) {
	base := c.Addressing.HoldingAddressBase
	if holdingBase != 0 {
		base = holdingBase
	}
	out := make([]ResolvedEntry, 0, len(c.Registers))
	for _, e := range c.Registers {
		if e.SwapType != "" && e.SwapType != SwapABCD_BE {
			return nil, fmt.Errorf("registers: unsupported swap_type %q for %q", e.SwapType, e.MetricKey)
		}
		wc, err := e.WordCountForType()
		if err != nil {
			return nil, err
		}
		pduStart := e.Address - base
		if pduStart < 0 {
			return nil, fmt.Errorf("registers: negative PDU for %q (address=%d base=%d)", e.MetricKey, e.Address, base)
		}
		if pduStart > 65535 {
			return nil, fmt.Errorf("registers: PDU start overflow for %q", e.MetricKey)
		}
		end := pduStart + int(wc) - 1
		if end > 65535 {
			return nil, fmt.Errorf("registers: PDU end overflow for %q", e.MetricKey)
		}
		out = append(out, ResolvedEntry{
			Entry:     e,
			PDUStart:  uint16(pduStart),
			WordCount: wc,
			PDUEnd:    uint16(end),
		})
	}
	return out, nil
}

// Subset returns the entries in `all` whose MetricKey is listed in `keys`,
// preserving the catalog ordering. It returns an error listing every key
// that does not appear in the catalog so misconfigurations surface early.
// An empty `keys` slice returns the input unchanged so callers can use
// Subset(all, nil) to mean "no whitelist".
func Subset(all []ResolvedEntry, keys []string) ([]ResolvedEntry, error) {
	if len(keys) == 0 {
		return all, nil
	}
	wanted := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		wanted[k] = struct{}{}
	}
	out := make([]ResolvedEntry, 0, len(wanted))
	seen := make(map[string]struct{}, len(wanted))
	for _, e := range all {
		if _, ok := wanted[e.MetricKey]; !ok {
			continue
		}
		out = append(out, e)
		seen[e.MetricKey] = struct{}{}
	}
	if len(seen) != len(wanted) {
		missing := make([]string, 0, len(wanted)-len(seen))
		for k := range wanted {
			if _, ok := seen[k]; !ok {
				missing = append(missing, k)
			}
		}
		return nil, fmt.Errorf("registers: unknown metric_keys: %s", strings.Join(sortedStrings(missing), ", "))
	}
	return out, nil
}

func sortedStrings(s []string) []string {
	cp := append([]string(nil), s...)
	sort.Strings(cp)
	return cp
}

// Load reads a YAML catalog from path.
func Load(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	for i := range c.Registers {
		c.Registers[i].MetricKey = strings.TrimSpace(c.Registers[i].MetricKey)
		if c.Registers[i].MetricKey == "" {
			return nil, fmt.Errorf("registers: entry %d missing metric_key", i)
		}
	}
	return &c, nil
}
