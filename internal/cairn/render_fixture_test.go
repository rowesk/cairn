package cairn

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
)

// reportFixture exercises every element the Markdown templates draw: all
// heading levels, lists, a quote, callouts, footnotes, narrow and wide
// tables, code with and without a language, figures, a diagram, a bar and a
// line chart, the raw HTML Cairn keeps and some it removes. ´ stands for a
// backtick, which a Go raw string cannot hold.
var reportFixture = strings.ReplaceAll(`# Bulbs for the bedroom

A warm LED is the right bulb for a bedroom lamp. It uses a seventh of the power of a halogen and gives a softer light late at night.[^lumens]

## What the bulbs cost

Running cost matters more than the price on the box. A bulb that is on four hours a day for ten years costs far more to run than to buy.[^rate] Use ´--compare´ to see the figures side by side.

| Bulb | Power | Brightness | Price |
|---|---:|---:|---:|
| Warm LED | 6 W | 800 lm | £4.50 |
| Halogen | 42 W | 630 lm | £2.10 |
| Smart LED | 9 W | 806 lm | £14.99 |

### Every bulb, side by side

| Bulb | Fitting | Colour | Dimmable | Lifetime | Warranty | Notes |
|---|---|---|---|---|---|---|
| Warm LED | E27 | Warm white | Yes | 25,000 h | Two years | No flicker at low brightness |
| Halogen | E27 | Warm white | Yes | 2,000 h | None | Hot to the touch<br>after a minute |
| Smart LED | E27 | Any colour | Yes | 25,000 h | Two years | Needs the maker's app and a hub |
| Filament LED | E14 | Extra warm | No | 15,000 h | One year | Looks like an old bulb |
| Compact fluorescent | B22 | Cool white | No | 8,000 h | One year | Slow to reach full brightness |
| Reading LED | GU10 | Neutral white | Yes | 25,000 h | Three years | Narrow beam for a reading lamp |
| Candle LED | E14 | Warm white | Yes | 20,000 h | Two years | Fits the bedside sconce |
| Daylight LED | B22 | Daylight | No | 25,000 h | Two years | Too blue for a bedroom |

#### How the lifetime was measured

Makers quote lifetime at 70 per cent of the starting brightness.

##### A note on colour

Warm white is about 2,700 kelvin.

###### Source of the colour figures

The figures come from each maker's data sheet.

## How to choose

1. Measure the lamp's fitting.
2. Pick a colour:
   - Warm white for bedrooms
   - Neutral white for desks
     1. Check the beam angle
     2. Check the glare rating
3. Check the dimmer works with LEDs.

- [x] Fitting measured
- [ ] Dimmer checked

> The installer said: "Every LED I fit now is warm white, because nobody likes the blue ones after a week."

> [!VERDICT]
> Buy the warm LED. It pays for itself in under a year and the light is kinder at night.

> [!WARNING]
> Old dimmer switches can make LEDs flicker. Check yours before buying six.

## The order to do it in

´´´mermaid
flowchart LR
  A[Measure the fitting] --> B[Choose the colour]
  B --> C[Check the dimmer]
  C --> D{Flickers?}
  D -- yes --> E[Replace the dimmer]
  D -- no --> F[Fit every bulb]
´´´

## Cost over ten years

´´´echarts
{"title":{"text":"Ten-year running cost"},"tooltip":{},"xAxis":{"type":"category","data":["Warm LED","Halogen","Smart LED","Filament LED","Compact fluorescent"]},"yAxis":{"type":"value","axisLabel":{"formatter":"£{value}"}},"series":[{"type":"bar","name":"Running cost","data":[41,286,76,54,98]}]}
´´´

´´´echarts
{"legend":{},"xAxis":{"type":"category","data":["Year 1","Year 2","Year 3","Year 4","Year 5","Year 6","Year 7","Year 8","Year 9","Year 10"]},"yAxis":{"type":"value","axisLabel":{"formatter":"£{value}"}},"series":[{"type":"line","name":"Warm LED","data":[9,13,17,21,25,29,33,37,41,45]},{"type":"line","name":"Halogen","data":[31,60,89,118,147,176,205,234,263,292]}]}
´´´

## Pictures

![A warm LED bulb glowing on a bedside table](?asset=bulb.png "The warm LED at full brightness")

The other two bulbs, photographed in the same lamp:

![The halogen bulb](?asset=bulb.png "Halogen")

![The smart bulb](?asset=bulb.png "Smart LED")

## Code

´´´sh
curl -s https://example.com/api/bulbs --header "Accept: application/json" | jq '.bulbs[] | select(.watts < 10) | .name'
´´´

´´´
Bulb      Watts  Lumens
Warm LED  6      800
Halogen   42     630
´´´

´´´text
A long line of plain notes that should wrap on a phone because it is prose rather than code, and wrapping it helps the reader more than scrolling would.
´´´

## Small print

Water is H<sub>2</sub>O and the area is r<sup>2</sup>. Save with <kbd>Cmd</kbd>+<kbd>S</kbd>. The <mark>warm LED</mark> wins.<br>This sentence starts on a new line.

<details>
<summary>How the running cost was worked out</summary>

Each bulb's power times four hours a day, at **28p** a kilowatt hour.

</details>

<img src="?asset=bulb.png" width="120" alt="A small bulb">

<figure>
<img src="?asset=bulb.png" alt="The bulb from above">
<figcaption>The bulb from above, with its fitting.</figcaption>
</figure>

<script>alert("script")</script>
<iframe src="https://example.com/frame"></iframe>
<p style="color:red" onclick="alert('click')">A paragraph with a style and a click handler.</p>
<a href="javascript:alert('link')">A link that runs code</a>
<form action="https://example.com"><input type="text" name="q"><button>Send</button></form>

---

That is everything for the bedroom.

[^lumens]: Lumens measure how much light a bulb gives out. A 60 W filament bulb gave about 800.
[^rate]: At 28p a kilowatt hour, the rate on the bill in November 2026.
`, "´", "`")

// fixtureImage is a small warm gradient PNG for the fixture's pictures.
func fixtureImage() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 480, 320))
	for y := 0; y < 320; y++ {
		for x := 0; x < 480; x++ {
			dx, dy := float64(x-240)/240, float64(y-150)/170
			glow := 1 - (dx*dx+dy*dy)/1.4
			if glow < 0 {
				glow = 0
			}
			img.Set(x, y, color.RGBA{uint8(52 + 200*glow), uint8(62 + 150*glow), uint8(74 + 60*glow), 255})
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}
