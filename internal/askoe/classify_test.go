package askoe

import "testing"

func TestClassifyFilename(t *testing.T) {
	cases := map[string]Channel{
		"08 Погодинна А+ ЕЕ РУ-10 Серпень ЖЕ.xls": ChannelImport,
		"08 Погодинна А- ЕЕ РУ-10 Серпень ЖЕ.xls": ChannelExport,
		"08 Погодинна А- ЕЕ СЕС Серпень ЖЕ.xls":   ChannelPV,
		"РУ-10 лютий подобове АСКОЕ ЖЕ.xls":      ChannelUnknown,
		"random.xls":                             ChannelUnknown,
	}
	for name, want := range cases {
		if got := ClassifyFilename(name); got != want {
			t.Errorf("%s: got %s want %s", name, got, want)
		}
	}
}
