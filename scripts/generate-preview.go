//go:build ignore

// Run from the repository root: go run scripts/generate-preview.go
// Generates the committed card once, using the mark in ui.go and tokens.css colours.
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"sort"
)

type point struct{ x, y float64 }

func main() {
	const scale = 2
	img := image.NewRGBA(image.Rect(0, 0, 1200*scale, 630*scale))
	cover := color.RGBA{0x29, 0x48, 0x5f, 255}
	ink := color.RGBA{0xf2, 0xf6, 0xf8, 255}
	draw.Draw(img, img.Bounds(), image.NewUniform(cover), image.Point{}, draw.Src)
	// A paper edge and two quiet rules keep the card in the reader's palette.
	rect := func(x, y, w, h int, c color.RGBA) {
		draw.Draw(img, image.Rect(x*scale, y*scale, (x+w)*scale, (y+h)*scale), image.NewUniform(c), image.Point{}, draw.Src)
	}
	rect(0, 606, 1200, 24, color.RGBA{0xee, 0xf2, 0xf5, 255})
	rect(90, 92, 1020, 2, color.RGBA{0x52, 0x69, 0x7c, 255})
	rect(90, 536, 1020, 2, color.RGBA{0x52, 0x69, 0x7c, 255})
	// The three closed cubic paths reproduce the Cairn mark, enlarged.
	paths := [][]point{
		{{11, 3}, {14, 0}, {21, 1}, {21, 5}, {21, 8}, {13, 10}, {10, 8}, {7, 7}, {8, 5}, {11, 3}},
		{{7, 13}, {11, 10}, {22, 10}, {24, 14}, {26, 18}, {15, 20}, {9, 19}, {3, 19}, {2, 16}, {7, 13}},
		{{5, 24}, {11, 21}, {24, 21}, {27, 25}, {30, 30}, {20, 33}, {10, 32}, {0, 31}, {-3, 28}, {5, 24}},
	}
	for _, path := range paths {
		var points []point
		for i := 1; i < len(path); i += 3 {
			a, b, c, d := path[i-1], path[i], path[i+1], path[i+2]
			for j := 0; j < 80; j++ {
				t := float64(j) / 80
				u := 1 - t
				points = append(points, point{(495 + 7*(u*u*u*a.x+3*u*u*t*b.x+3*u*t*t*c.x+t*t*t*d.x)) * scale, (190 + 7*(u*u*u*a.y+3*u*u*t*b.y+3*u*t*t*c.y+t*t*t*d.y)) * scale})
			}
		}
		for y := 0; y < img.Bounds().Dy(); y++ {
			var xs []float64
			for i, a := range points {
				b := points[(i+1)%len(points)]
				fy := float64(y) + .5
				if (a.y <= fy && b.y > fy) || (b.y <= fy && a.y > fy) {
					xs = append(xs, a.x+(fy-a.y)*(b.x-a.x)/(b.y-a.y))
				}
			}
			sort.Float64s(xs)
			for i := 0; i+1 < len(xs); i += 2 {
				draw.Draw(img, image.Rect(int(math.Ceil(xs[i])), y, int(math.Ceil(xs[i+1])), y+1), image.NewUniform(ink), image.Point{}, draw.Src)
			}
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, 1200, 630))
	for y := 0; y < 630; y++ {
		for x := 0; x < 1200; x++ {
			var r, g, b uint32
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					c := img.RGBAAt(x*scale+dx, y*scale+dy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / 4), uint8(g / 4), uint8(b / 4), 255})
		}
	}
	f, err := os.Create("internal/cairn/ui/preview.png")
	if err != nil {
		panic(err)
	}
	if err = png.Encode(f, out); err != nil {
		panic(err)
	}
	if err = f.Close(); err != nil {
		panic(err)
	}
}
