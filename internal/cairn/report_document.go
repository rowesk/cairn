package cairn

import _ "embed"

//go:embed ui/report-size.js
var reportSizeJS string

// Report messages carry only a height or an in-page scroll offset. The reader validates the sending frame.
func reportDocument(content string) string {
	content, _ = reportHeadings(content)
	return reportLinks(content) + "\n<script>" + reportSizeJS + "</script>"
}
