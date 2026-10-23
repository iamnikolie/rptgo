package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/signintech/gopdf"
)

// The em sizes Crystal's PDF export draws Arial at.
func TestFontSetting(t *testing.T) {
	cases := []struct {
		size float64
		bold bool
		em   float64
	}{{8, false, 7.15}, {10, false, 8.95}, {11, false, 9.85}, {12, false, 10.75}, {8, true, 6.95}}
	for _, c := range cases {
		em, _, _ := font{name: "Arial", size: c.size, bold: c.bold}.setting()
		if em != c.em {
			t.Errorf("%gpt bold=%v: em %g, want %g", c.size, c.bold, em, c.em)
		}
	}
}

func TestPDFWriterItems(t *testing.T) {
	dir := "/usr/share/fonts/truetype/liberation"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no Liberation fonts")
	}
	w := &pdfWriter{pdf: &gopdf.GoPdf{}, fontDir: dir, loaded: map[string]bool{}, pageW: 792}
	w.pdf.Start(gopdf.Config{Unit: gopdf.UnitPT, PageSize: gopdf.Rect{W: 792, H: 612}})
	w.pdf.AddPage()
	items := []item{
		{kind: itemText, x: 200, y: 200, w: 2000, h: 240, text: "Page " + pageSlot + " of " + totalSlot, align: "Right",
			font: font{name: "Arial", size: 8}},
		{kind: itemText, x: 200, y: 600, w: 300, h: 240, text: "CLIPPED TEXT", align: "Left",
			font: font{name: "Times New Roman", size: 10, bold: true}},
		{kind: itemFill, x: 100, y: 100, w: 20, h: 2000},
		{kind: itemBox, x: 100, y: 100, w: 2000, h: 2000},
		{kind: itemBorder, x: 100, y: 100, w: 2000, h: 300, border: borders{top: 2, bottom: 1, width: 1}},
	}
	for _, it := range items {
		if err := w.item(it, 3, 7); err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	if _, err := w.pdf.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b.Bytes(), []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	if line, _, _ := w.fit("CLIPPED TEXT", 15); line == "" || line == "CLIPPED TEXT" {
		t.Errorf("fit kept %q of a 15pt box", line)
	}
}
