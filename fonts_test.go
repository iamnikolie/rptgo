package main

import "testing"

// Clip points measured on a Crystal PDF export. The letters are swapped for
// others of the same advance width, so the widths are the measured ones.
func TestFitTextMatchesMeasuredClips(t *testing.T) {
	regular := Format{FontName: "Arial", FontSize: 8}
	cases := []struct {
		f     Format
		width int
		in    string
		want  string
	}{
		{regular, 3192, "LKNNR Khmiqiyfrefizn Wurxturvn Dnalevnmnqf Plho", "LKNNR Khmiqiyfrefizn Wurxturvn Dnalevnmnqf Plh"},
		{regular, 3192, "Luy Duglny - Curfp Tuwnr (YXN 7R + Cnw Vifvpnq)", "Luy Duglny - Curfp Tuwnr (YXN 7R + Cnw Vifvpnq)"},
		{regular, 3192, "Luy Kqonlny Gnqnrel Mnh - DNA X8 Xykv YKN - KNT", "Luy Kqonlny Gnqnrel Mnh - DNA X8 Xykv YKN - K"},
		{regular, 3192, "Seyf Luy Kqonlny Nullnon - Yudfp Gefn Nemady   ", "Seyf Luy Kqonlny Nullnon - Yudfp Gefn Nemady"},
	}
	for _, c := range cases {
		if got := fitText(c.in, c.f, c.width); got != c.want {
			t.Errorf("fitText(%q) = %q (width %.0f of %d), want %q (width %.0f)",
				c.in, got, textWidth(got, c.f), c.width, c.want, textWidth(c.want, c.f))
		}
	}
}
