package main

// Pictures embedded in a report: decoded once, drawn scaled to their box.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"

	_ "golang.org/x/image/bmp"

	"github.com/signintech/gopdf"
)

type picture struct {
	img image.Image
	png string // the picture as PNG, base64, for HTML
}

// picture decodes an object's embedded picture; nil when it has none or it
// is in a format Go does not read (WMF, EMF).
func (e *Engine) picture(o Object) *picture {
	if p, ok := e.pics[o.Name]; ok {
		return p
	}
	var p *picture
	if len(o.ImageData) > 0 {
		img, _, err := image.Decode(bytes.NewReader(o.ImageData))
		if err != nil {
			e.errs[fmt.Sprintf("picture %s not drawn: %v", o.Name, err)]++
		} else {
			p = &picture{img: img}
		}
	}
	e.pics[o.Name] = p
	return p
}

func (w *pdfWriter) image(it item) error {
	if it.image == nil {
		return nil
	}
	return w.pdf.ImageFrom(it.image.img, tw(it.x), tw(it.y), &gopdf.Rect{W: tw(it.w), H: tw(it.h)})
}

func htmlImage(it item) string {
	p := it.image
	if p == nil {
		return ""
	}
	if p.png == "" {
		var b bytes.Buffer
		if err := png.Encode(&b, p.img); err != nil {
			return ""
		}
		p.png = base64.StdEncoding.EncodeToString(b.Bytes())
	}
	return fmt.Sprintf(`<img class="b" alt="" style="left:%s;top:%s;width:%s;height:%s" src="data:image/png;base64,%s">`,
		pt(it.x), pt(it.y), pt(it.w), pt(it.h), html.EscapeString(p.png))
}
