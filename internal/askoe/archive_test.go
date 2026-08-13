package askoe

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractWorkbooksZip(t *testing.T) {
	xls, err := os.ReadFile(filepath.Join("testdata", "aug2024_import.xls"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("folder/08 Погодинна А+ ЕЕ РУ-10 Серпень ЖЕ.xls")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(xls); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := ExtractWorkbooks("dump.zip", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files=%d", len(files))
	}
	if ClassifyFilename(files[0].Name) != ChannelImport {
		t.Errorf("classified %s as %s", files[0].Name, ClassifyFilename(files[0].Name))
	}
}

func TestExtractWorkbooksLooseXLS(t *testing.T) {
	xls, err := os.ReadFile(filepath.Join("testdata", "aug2024_pv.xls"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractWorkbooks("08 Погодинна А- ЕЕ СЕС Серпень ЖЕ.xls", xls)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || ClassifyFilename(files[0].Name) != ChannelPV {
		t.Fatalf("got %+v", files)
	}
}
