# Changelog

## v0.2.0

- Saved preset themes (Cairn, Minimal, Futuristic) for the admin view, private reader and public reader.
- One page scroll in the reader at every width: a shorter cover band, a pinned bar with the title, current section and page actions, and a page-details sheet with the byline and full request.
- Nested contents list from report headings, set in the report's left margin beside its text when there is room and opened from the bar otherwise, with current-section tracking and `#section` deep links.
- Headings without ids get stable GitHub-style ids when served, so Markdown section links work.
- Bundles Mermaid 12.2.0, which ships DOMPurify 3.4.17 and KaTeX 0.18.10 and clears the critical advisories npm reported against Mermaid 11.17.2.
- Builds with Go 1.26.10, which fixes standard-library vulnerabilities in `html/template`, `net/http` and `crypto/x509` reported by govulncheck.
- Fixed in-report section links that did nothing on phones, and runaway frame heights for HTML reports without a doctype.

## v0.1.0

Initial public release under the MIT licence.

- Private searchable library, report previews and responsive readers.
- Markdown editor, file imports, original downloads and managed images.
- Sandboxed HTML, packaged Mermaid diagrams and ECharts charts.
- Per-agent publishing keys, author profiles and private request summaries.
- Optional public sharing and passwords, with matching private/public paths.
- Three-character mixed-case alphanumeric paths by default.
- Idempotent owner/credential setup and Linux AMD64/ARM64 release archives.

Existing personal installations retain their data, credentials, preferences and URLs. This release introduces no incompatible database migration.
