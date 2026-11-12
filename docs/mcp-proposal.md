# Proposal: `cairn mcp`

Status: proposal for the owner to decide. Nothing here is built.

## What it would be

A `cairn mcp` subcommand that speaks the Model Context Protocol over standard input and output. An agent's client starts it as a child process; it turns each tool call into one request to the publishing API and returns the JSON answer. It holds no state and opens no port, and the server side stays exactly as it is: the same routes, scope rules, limits and errors that `docs/publishing.md` describes.

Today an agent reads the setup text and writes HTTP requests itself, or runs `cairn publish`. MCP would let clients that support it see Cairn as a set of typed tools instead, with the field limits in each tool's input schema, so the model never builds JSON or base64 by hand.

## Tools

Each maps to one route that exists now, and inherits the agent key's API scope: only pages with its byline. Like the key, it is not a lock: the private address has no sign-in, so the machine running the MCP server must be one the owner trusts.

| Tool | Route | Input |
| --- | --- | --- |
| `publish_page` | `POST /api/pages` | `title`, `original_request`, `request_summary`, `request_source`, `markdown` or `html`, `template`, `slug`, and `images` as local file paths that the server reads and encodes |
| `replace_page` | `PUT /api/pages/{slug}` | `slug` and the same fields |
| `list_pages` | `GET /api/pages` | `q`, `archived`, `limit`, `cursor` |
| `read_page` | `GET /api/pages/{slug}` | `slug`. Returns the kept Markdown so the model can edit it |
| `archive_page` | `PATCH /api/pages/{slug}` | `slug`, `archived` |

No sharing or deleting tools, as in the API. Each write sends an `Idempotency-Key` that the tool derives from the call, so a client that retries a call never saves twice. Field errors come back as the tool's error result with the API's `errors` list intact, which models handle well. The input schemas come from the same `publicationFields` table that builds `/api/openapi.json`, so the tools cannot drift from the API.

A resource, `cairn://setup`, would serve the agent setup text, so the client can load the when-to-publish guidance without the owner pasting it.

## Installing it

Claude Code, for one user across projects:

```sh
claude mcp add cairn --scope user \
  --env CAIRN_URL=https://cairn.example.ts.net \
  --env CAIRN_API_KEY_FILE=$HOME/.config/cairn/claude-code.key \
  -- cairn mcp
```

Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.cairn]
command = "cairn"
args = ["mcp"]
env = { CAIRN_URL = "https://cairn.example.ts.net", CAIRN_API_KEY_FILE = "/Users/me/.config/cairn/codex.key" }
```

Both clients start `cairn mcp` when a session begins and stop it at the end.

## Where the key lives

Not in either config file: people commit and share those. The key lives in a file only its user can read (`chmod 600`), and the config names the file with `CAIRN_API_KEY_FILE`, the way `CAIRN_PUBLISHER_TOKEN_FILE` names the installation token for `cairn serve`. `CAIRN_API_KEY` would work too, but MCP clients do not all pass their whole environment to the servers they start, and a variable set in the config file is the secret in the config again. `cairn mcp` would refuse to start when both are set, and say which to unset, as `serve` does for its own secrets.

Each client gets its own key from Settings, so its pages carry its own byline and revoking one does not stop the others. `cairn publish` should learn `CAIRN_API_KEY_FILE` at the same time.

## What it costs

Standard library only. MCP over stdio is JSON-RPC 2.0, one JSON object per line: `initialize`, `tools/list`, `tools/call`, `resources/list`, `resources/read` and a few notifications. `encoding/json`, `bufio` and the HTTP client `cairn publish` already uses cover all of it. No SDK is needed and `go.mod` does not change.

Estimate: about 450 lines of Go in `cmd/cairn/mcp.go`, including the key file, most of it the tool schemas and the mapping from API errors to tool results, plus about 300 lines of tests that drive the protocol over pipes against a real loopback server, as `publish_test.go` does. Two to three days including docs and help. The binary grows by tens of kilobytes.

Ongoing cost is small but real: the MCP specification still changes between versions, so each Cairn release should be tried against the current Claude Code and Codex. Nothing on the server changes, so a broken `cairn mcp` never affects the library or the API.

## Recommendation

Build it once the API has settled through one release. Until then, the setup text and `cairn publish` cover the same ground for every client, with or without MCP support.
