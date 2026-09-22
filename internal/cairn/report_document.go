package cairn

import _ "embed"

//go:embed ui/report-size.js
var reportSizeJS string

// Sizing messages carry only a height. The reader validates the sending frame.
func reportDocument(content string) string {
	return reportLinks(content) + "\n<script>" + reportSizeJS + "</script>"
}
