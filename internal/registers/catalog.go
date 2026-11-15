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
	DTUint16  DataType = "UINT16"
	DTUint32  DataType = "UINT32"
	DTInt32   DataType = "INT32"
	DTInt64   DataType = "INT64"
	DTUint64  DataType = "UINT64"
	DTFloat32 DataType = "FLOAT32" // IEEE 754 single precision
)

// ScopeEdge marks entries only the on-site edge controller reads, and
// only when a device whitelists them by name. Some firmwares answer
// these with a Modbus exception (or they sit behind another unit id),
// which would fail a full-catalog collector's whole poll.
const ScopeEdge = "edge"

// Entry is one logical metric in the catalog.
type Entry struct {
	MetricKey string   `yaml:"metric_key"`
	Name      string   `yaml:"name"`
	Address   int      `yaml:"address"` // vendor-documented address
	DataType  DataType `yaml:"data_type"`
	SwapType  SwapType `yaml:"swap_type"`
	Gain      float64  `yaml:"gain"`
	Offset    float64  `yaml:"offset"`
	Scope     string   `yaml:"scope"` // "" = everywhere, ScopeEdge = explicit edge whitelist only
	// Bitfield marks a register of on/off bits. Every pattern is a
	// reading there, all ones included, so the "no value" sentinel
	// check does not apply.
	Bitfield bool `yaml:"bitfield"`
}

// Device names the kind of device a catalog maps. Catalogs that a
// single modbus_devices[] entry brings along (its register_catalog)
// set it; the main SmartLogger catalog does not.
type Device struct {
	// Name labels the device in alert emails.
	Name string `yaml:"name"`
	// ProbeMetricKey is a register written on every poll. The alert
	// watchdog judges the device's freshness by it.
	ProbeMetricKey string `yaml:"probe_metric_key"`
}

// ReadBlock is a register range the device is known to answer in a
// single request, the registers between the catalog's entries included.
// Start uses the same addressing as Entry.Address.
type ReadBlock struct {
	Start int `yaml:"start"`
	Count int `yaml:"count"`
}

// Catalog is the full register map plus addressing defaults.
type Catalog struct {
	Device     Device `yaml:"device"`
	Addressing struct {
		HoldingAddressBase int `yaml:"holding_address_base"`
	} `yaml:"addressing"`
	// ReadBlocks lets entries that are not adjacent share a request.
	// Without them every run of adjacent registers is its own read.
	ReadBlocks []ReadBlock `yaml:"read_blocks"`

	Registers []Entry `yaml:"registers"`
}

// maxBlockRegisters is the Modbus limit on registers in one read.
const maxBlockRegisters = 125

// ResolvedEntry has computed PDU start and register width.
type ResolvedEntry struct {
	Entry
	PDUStart  uint16
	WordCount uint16
	PDUEnd    uint16 // inclusive last register index
	// ReadBlock is 1 + the index of the catalog read block the entry
	// lies in, 0 for none. Entries of one block are read together.
	ReadBlock int
}

func (e Entry) WordCountForType() (uint16, error) {
	switch e.DataType {
	case DTUint16:
		return 1, nil
	case DTUint32, DTInt32, DTFloat32:
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
	blocks, err := c.resolveBlocks(base)
	if err != nil {
		return nil, err
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
		block, err := blockOf(blocks, pduStart, end)
		if err != nil {
			return nil, fmt.Errorf("registers: %q %w", e.MetricKey, err)
		}
		out = append(out, ResolvedEntry{
			Entry:     e,
			PDUStart:  uint16(pduStart),
			WordCount: wc,
			PDUEnd:    uint16(end),
			ReadBlock: block,
		})
	}
	return out, nil
}

// pduBlock is a resolved read block, both ends inclusive.
type pduBlock struct{ start, end int }

func (c *Catalog) resolveBlocks(base int) ([]pduBlock, error) {
	out := make([]pduBlock, 0, len(c.ReadBlocks))
	for i, b := range c.ReadBlocks {
		if b.Count < 1 || b.Count > maxBlockRegisters {
			return nil, fmt.Errorf("registers: read_blocks[%d] count must be 1..%d, got %d", i, maxBlockRegisters, b.Count)
		}
		start := b.Start - base
		end := start + b.Count - 1
		if start < 0 || end > 65535 {
			return nil, fmt.Errorf("registers: read_blocks[%d] lies outside the register space", i)
		}
		for j, o := range out {
			if start <= o.end && o.start <= end {
				return nil, fmt.Errorf("registers: read_blocks[%d] overlaps read_blocks[%d]", i, j)
			}
		}
		out = append(out, pduBlock{start: start, end: end})
	}
	return out, nil
}

// blockOf returns 1 + the index of the block holding [start, end], or 0
// when none does. An entry half inside a block is rejected: the block
// was most likely mistyped.
func blockOf(blocks []pduBlock, start, end int) (int, error) {
	for i, b := range blocks {
		startIn := start >= b.start && start <= b.end
		endIn := end >= b.start && end <= b.end
		switch {
		case startIn && endIn:
			return i + 1, nil
		case startIn || endIn:
			return 0, fmt.Errorf("crosses the edge of read_blocks[%d]", i)
		}
	}
	return 0, nil
}

// Subset returns the entries in `all` whose MetricKey is listed in `keys`,
// preserving the catalog ordering. It returns an error listing every key
// that does not appear in the catalog so misconfigurations surface early.
// An empty `keys` slice means "no whitelist": every entry except the
// ScopeEdge ones, which are only ever read when named explicitly.
func Subset(all []ResolvedEntry, keys []string) ([]ResolvedEntry, error) {
	if len(keys) == 0 {
		out := make([]ResolvedEntry, 0, len(all))
		for _, e := range all {
			if e.Scope != ScopeEdge {
				out = append(out, e)
			}
		}
		return out, nil
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
	keys := make(map[string]bool, len(c.Registers))
	for i := range c.Registers {
		e := &c.Registers[i]
		e.MetricKey = strings.TrimSpace(e.MetricKey)
		if e.MetricKey == "" {
			return nil, fmt.Errorf("registers: entry %d missing metric_key", i)
		}
		if e.Bitfield && e.DataType != DTUint16 && e.DataType != DTUint32 {
			return nil, fmt.Errorf("registers: %q: bitfield needs UINT16 or UINT32, got %s", e.MetricKey, e.DataType)
		}
		keys[e.MetricKey] = true
	}
	c.Device.Name = strings.TrimSpace(c.Device.Name)
	c.Device.ProbeMetricKey = strings.TrimSpace(c.Device.ProbeMetricKey)
	if p := c.Device.ProbeMetricKey; p != "" && !keys[p] {
		return nil, fmt.Errorf("registers: device.probe_metric_key %q is not in the catalog", p)
	}
	return &c, nil
}
