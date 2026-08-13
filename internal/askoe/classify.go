package askoe

import (
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Channel is one commercial-meter series in an ASKOE dump.
type Channel int

const (
	ChannelUnknown Channel = iota
	ChannelImport          // A+ at the 10 kV connection (grid buy)
	ChannelExport          // A− at the 10 kV connection (grid sell)
	ChannelPV              // A− at the SES meter (generation)
)

func (c Channel) String() string {
	switch c {
	case ChannelImport:
		return "import"
	case ChannelExport:
		return "export"
	case ChannelPV:
		return "pv"
	default:
		return "unknown"
	}
}

// ClassifyFilename maps an ASKOE workbook name onto a channel. Daily
// (подобові) summaries are skipped — the hourly sheets already carry
// the same totals. Matching is case-insensitive and NFC-normalized so
// macOS NFD filenames ("Лютий") still classify.
func ClassifyFilename(name string) Channel {
	base := strings.ToLower(norm.NFC.String(filepath.Base(name)))
	base = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, base)
	if !strings.Contains(base, "погодинн") {
		return ChannelUnknown
	}
	isPlus := strings.Contains(base, "а+") || strings.Contains(base, "a+")
	isMinus := strings.Contains(base, "а-") || strings.Contains(base, "a-")
	isSES := strings.Contains(base, "сес")
	isRU := strings.Contains(base, "ру-10") || strings.Contains(base, "ру 10")
	switch {
	case isPlus && isRU:
		return ChannelImport
	case isMinus && isSES:
		return ChannelPV
	case isMinus && isRU:
		return ChannelExport
	default:
		return ChannelUnknown
	}
}
