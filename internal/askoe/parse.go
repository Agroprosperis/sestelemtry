package askoe

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HourGrid is one site's hourly kWh keyed by Kyiv civil day. A day is
// complete only when Import, Export and PV all have a row — missing
// export (July 2024 in the Zhmerynka dump) is not filled with zeros,
// because that would invent a no-export day.
type HourGrid struct {
	Import map[civilDay][24]float64
	Export map[civilDay][24]float64
	PV     map[civilDay][24]float64
}

type civilDay struct{ Y, M, D int }

func (d civilDay) Time(loc *time.Location) time.Time {
	return time.Date(d.Y, time.Month(d.M), d.D, 0, 0, 0, 0, loc)
}

func (d civilDay) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Y, d.M, d.D)
}

func dayOf(t time.Time) civilDay {
	y, m, d := t.Date()
	return civilDay{y, int(m), d}
}

// WorkbookFile is one .xls pulled out of an upload (loose file, zip, or 7z).
type WorkbookFile struct {
	Name string
	Data []byte
}

// ParseWorkbooks classifies and folds every hourly sheet into one grid.
// Unrecognized names are skipped (not an error) so a dump that also
// contains daily summaries still imports.
func ParseWorkbooks(files []WorkbookFile) (HourGrid, []string, error) {
	g := HourGrid{
		Import: map[civilDay][24]float64{},
		Export: map[civilDay][24]float64{},
		PV:     map[civilDay][24]float64{},
	}
	var warnings []string
	var parsed int
	for _, f := range files {
		ch := ClassifyFilename(f.Name)
		if ch == ChannelUnknown {
			continue
		}
		hours, err := parseHourlySheet(f.Data)
		if err != nil {
			return HourGrid{}, nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		if len(hours) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s: no day rows", f.Name))
			continue
		}
		dst := destMap(g, ch)
		for day, vals := range hours {
			dst[day] = vals
		}
		parsed++
	}
	if parsed == 0 {
		return HourGrid{}, warnings, fmt.Errorf("no hourly ASKOE sheets found (need Погодинна А+/А− РУ-10 and А− СЕС)")
	}
	return g, warnings, nil
}

func destMap(g HourGrid, ch Channel) map[civilDay][24]float64 {
	switch ch {
	case ChannelImport:
		return g.Import
	case ChannelExport:
		return g.Export
	case ChannelPV:
		return g.PV
	default:
		return nil
	}
}

// CompleteDays returns Kyiv civil days that have all three channels,
// sorted chronologically.
func (g HourGrid) CompleteDays() []civilDay {
	out := make([]civilDay, 0)
	for day := range g.Import {
		if _, ok := g.Export[day]; !ok {
			continue
		}
		if _, ok := g.PV[day]; !ok {
			continue
		}
		out = append(out, day)
	}
	sortDays(out)
	return out
}

func sortDays(days []civilDay) {
	for i := 1; i < len(days); i++ {
		for j := i; j > 0 && lessDay(days[j], days[j-1]); j-- {
			days[j], days[j-1] = days[j-1], days[j]
		}
	}
}

func lessDay(a, b civilDay) bool {
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	if a.M != b.M {
		return a.M < b.M
	}
	return a.D < b.D
}

func parseHourlySheet(payload []byte) (map[civilDay][24]float64, error) {
	cells, err := decodeXLS(payload)
	if err != nil {
		return nil, err
	}
	maxRow, maxCol := 0, 0
	for k := range cells {
		if k[0] > maxRow {
			maxRow = k[0]
		}
		if k[1] > maxCol {
			maxCol = k[1]
		}
	}
	header := -1
	for r := 0; r <= maxRow; r++ {
		c, ok := cells[[2]int{r, 0}]
		if !ok || c.kind != 's' {
			continue
		}
		if strings.Contains(strings.ToLower(c.str), "дата") {
			header = r
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("header row with Дата not found")
	}
	out := map[civilDay][24]float64{}
	for r := header + 1; r <= maxRow; r++ {
		day, ok := cellDate(cells[[2]int{r, 0}])
		if !ok {
			continue
		}
		var hours [24]float64
		for h := 0; h < 24; h++ {
			hours[h] = cellNumber(cells[[2]int{r, 2 + h}])
		}
		out[day] = hours
	}
	return out, nil
}

func cellDate(c cellValue) (civilDay, bool) {
	switch c.kind {
	case 'n':
		if c.num < 20000 || c.num > 80000 {
			return civilDay{}, false
		}
		// Excel 1900 date system: serial 1 = 1899-12-31; Go adds from 1899-12-30.
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(c.num))
		return dayOf(t), true
	case 's':
		s := strings.TrimSpace(c.str)
		for _, layout := range []string{"2006-01-02", "02.01.2006", "02/01/2006"} {
			if t, err := time.Parse(layout, s); err == nil {
				return dayOf(t), true
			}
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return cellDate(cellValue{kind: 'n', num: n})
		}
		return civilDay{}, false
	default:
		return civilDay{}, false
	}
}

func cellNumber(c cellValue) float64 {
	if c.kind == 'n' {
		return c.num
	}
	if c.kind == 's' {
		s := strings.TrimSpace(strings.ReplaceAll(c.str, ",", "."))
		if s == "" {
			return 0
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}
