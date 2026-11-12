package cairn_test

import (
	"strings"
	"testing"
)

func TestPublishingRejectsInvalidChartJSON(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Running costs")
	delete(in, "html")
	in["markdown"] = "# Running costs\n\n```echarts\n{\"series\": [{\"type\": \"bar\", \"data\": [1, 2,]}]}\n```"
	status, _, body := request(t, srv, "POST", "/api/pages", in, token)
	if status != 400 || !strings.Contains(string(body), "validation_failed") || !strings.Contains(string(body), `"code":"invalid_chart","field":"markdown"`) || !strings.Contains(string(body), "echarts block 1 on line 4") || !strings.Contains(string(body), `"line":4,"block":1`) {
		t.Fatalf("invalid chart: %d %s", status, body)
	}
	status, _, _ = request(t, srv, "GET", "/report", nil, "")
	if status != 404 {
		t.Fatalf("a rejected page was saved: %d", status)
	}
}

func TestMarkdownPageHidesTheRepeatedTitleAndKeepsItForContents(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	in := page("Bulb comparison: what to buy")
	delete(in, "html")
	in["markdown"] = "# Bulb comparison\n\nIntro.[^1]\n\n## Costs\n\n## Colour\n\n## Fittings\n\n[^1]: A note."
	status, _, body := request(t, srv, "POST", "/api/pages", in, token)
	if status != 201 {
		t.Fatalf("publish: %d %s", status, body)
	}
	_, _, content := request(t, srv, "GET", "/_content/report", nil, "")
	for _, want := range []string{`<h1 class="title-echo" id="bulb-comparison">Bulb comparison</h1>`, `href="#fn-1"`, `<li id="fn-1">`, `cairn:report-height`} {
		if !strings.Contains(string(content), want) {
			t.Errorf("content missing %s", want)
		}
	}
	_, _, reader := request(t, srv, "GET", "/report", nil, "")
	if strings.Contains(string(reader), `#bulb-comparison"`) || !strings.Contains(string(reader), `#costs"`) {
		t.Error("contents should start at the first section, not the repeated title")
	}
}
