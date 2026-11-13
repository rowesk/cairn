//go:build ignore

// Run from the repository root: go run scripts/generate-icons.go
// Draws the app and browser icons once, as committed PNGs: the Cairn mark
// from ui.go in the cover colours from tokens.css, centred on a full square
// so iOS can round the corners and Android can mask it to any shape.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"sort"
)

type point struct{ x, y float64 }

// The mark's three stones as closed cubic paths, in its 30x34 viewBox.
var stones = [][]point{
	{{11, 3}, {14, 0}, {21, 1}, {21, 5}, {21, 8}, {13, 10}, {10, 8}, {7, 7}, {8, 5}, {11, 3}},
	{{7, 13}, {11, 10}, {22, 10}, {24, 14}, {26, 18}, {15, 20}, {9, 19}, {3, 19}, {2, 16}, {7, 13}},
	{{5, 24}, {11, 21}, {24, 21}, {27, 25}, {30, 30}, {20, 33}, {10, 32}, {0, 31}, {-3, 28}, {5, 24}},
}

func main() {
	for _, size := range []int{32, 180, 192, 512} {
		write(fmt.Sprintf("internal/cairn/ui/icon-%d.png", size), draw1(size))
	}
}

// draw1 renders one square icon, four times oversized and then averaged
// down so the stones' edges are smooth.
func draw1(size int) *image.RGBA {
	const scale = 4
	cover := color.RGBA{0x29, 0x48, 0x5f, 255}
	ink := color.RGBA{0xf2, 0xf6, 0xf8, 255}
	big := size * scale
	img := image.NewRGBA(image.Rect(0, 0, big, big))
	draw.Draw(img, img.Bounds(), image.NewUniform(cover), image.Point{}, draw.Src)
	// The stones span about x 0.5 to 27.5 and y 1.5 to 32.5. Small icons get a
	// bigger mark so it stays legible; large ones keep it inside the circle
	// Android's maskable icons may crop to.
	share := 0.56
	if size <= 32 {
		share = 0.8
	}
	unit := float64(big) * share / 31
	ox := float64(big)/2 - 14*unit
	oy := float64(big)/2 - 17*unit
	for _, path := range stones {
		var points []point
		for i := 1; i < len(path); i += 3 {
			a, b, c, d := path[i-1], path[i], path[i+1], path[i+2]
			for j := 0; j < 120; j++ {
				t := float64(j) / 120
				u := 1 - t
				x := u*u*u*a.x + 3*u*u*t*b.x + 3*u*t*t*c.x + t*t*t*d.x
				y := u*u*u*a.y + 3*u*u*t*b.y + 3*u*t*t*c.y + t*t*t*d.y
				points = append(points, point{ox + unit*x, oy + unit*y})
			}
		}
		for y := 0; y < big; y++ {
			var xs []float64
			fy := float64(y) + .5
			for i, a := range points {
				b := points[(i+1)%len(points)]
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
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b uint32
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					c := img.RGBAAt(x*scale+dx, y*scale+dy)
					r += uint32(c.R)
					g += uint32(c.G)
					b += uint32(c.B)
				}
			}
			n := uint32(scale * scale)
			out.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255})
		}
	}
	return out
}

func write(name string, img image.Image) {
	f, err := os.Create(name)
	if err != nil {
		panic(err)
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err = enc.Encode(f, img); err != nil {
		panic(err)
	}
	if err = f.Close(); err != nil {
		panic(err)
	}
}
