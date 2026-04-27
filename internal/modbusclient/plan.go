package modbusclient

import (
	"os"
	"strconv"
	"sort"

	"github.com/nesh/sestelemetry/internal/registers"
)

const maxRegistersPerRead = 125

// ReadChunk is one Modbus read of contiguous holding/input registers.
type ReadChunk struct {
	Start    uint16
	Quantity uint16
}

// PlanChunks starts with one read per catalog entry (never splits a multi-register value),
// then merges adjacent reads into a single FC3/FC4 request when the combined length is <= maxRegistersPerRead.
func PlanChunks(entries []registers.ResolvedEntry) []ReadChunk {
	if len(entries) == 0 {
		return nil
	}
	sorted := append([]registers.ResolvedEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].PDUStart == sorted[j].PDUStart {
			return sorted[i].PDUEnd < sorted[j].PDUEnd
		}
		return sorted[i].PDUStart < sorted[j].PDUStart
	})
	chunks := make([]ReadChunk, 0, len(sorted))
	for _, e := range sorted {
		chunks = append(chunks, ReadChunk{Start: e.PDUStart, Quantity: e.WordCount})
	}
	return mergeAdjacentChunks(chunks, configuredMaxRegistersPerRead())
}

func mergeAdjacentChunks(in []ReadChunk, maxPerRead int) []ReadChunk {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	out := []ReadChunk{in[0]}
	for _, c := range in[1:] {
		last := &out[len(out)-1]
		if int(c.Start) == int(last.Start)+int(last.Quantity) && int(last.Quantity)+int(c.Quantity) <= maxPerRead {
			last.Quantity += c.Quantity
			continue
		}
		out = append(out, c)
	}
	return out
}

func configuredMaxRegistersPerRead() int {
	v := os.Getenv("SESTELEMETRY_MAX_REGISTERS_PER_READ")
	if v == "" {
		return maxRegistersPerRead
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > maxRegistersPerRead {
		return maxRegistersPerRead
	}
	return n
}

// SliceForEntry returns the sub-slice of chunkBytes for a given entry within a read starting at chunkStart.
func SliceForEntry(chunkStart uint16, chunkBytes []byte, e registers.ResolvedEntry) []byte {
	off := int(e.PDUStart-chunkStart) * 2
	need := int(e.WordCount) * 2
	if off < 0 || off+need > len(chunkBytes) {
		return nil
	}
	return chunkBytes[off : off+need]
}
