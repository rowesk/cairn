# Changelog

## Unreleased

- Saved preset themes (Cairn, Minimal, Futuristic) for the admin view, private reader and public reader.
- One page scroll in the reader at every width: a shorter cover band, a bar that hides while reading and returns on scroll-up, and a page-details sheet with the byline and full request.
- Nested contents list from report headings: a sticky rail from 1200px, a sheet on smaller screens, current-section tracking and `#section` deep links.
- Headings without ids get stable GitHub-style ids when served, so Markdown section links work.
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
