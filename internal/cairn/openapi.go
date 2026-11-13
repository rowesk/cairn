package cairn

import (
	"reflect"
	"strconv"
	"strings"
)

// The OpenAPI 3.1 document at /api/openapi.json. Response schemas come from
// the Go structs the handlers encode, request schemas from publicationFields
// and the limits in api.go, so the document cannot describe fields or limits
// the server does not have. contract_test.go checks real responses against
// it. It names no host, owner, key or page.

// components maps the response structs to their schema names.
var components = []struct {
	name string
	t    reflect.Type
}{
	{"Page", reflect.TypeOf(apiPage{})},
	{"PageRead", reflect.TypeOf(apiPageRead{})},
	{"PageWrite", reflect.TypeOf(apiPageWrite{})},
	{"PageList", reflect.TypeOf(apiList{})},
	{"ListItem", reflect.TypeOf(apiListItem{})},
	{"Match", reflect.TypeOf(apiMatch{})},
	{"Source", reflect.TypeOf(apiSource{})},
	{"Warning", reflect.TypeOf(apiWarning{})},
	{"Error", reflect.TypeOf(APIError{})},
	{"FieldError", reflect.TypeOf(FieldError{})},
	{"Health", reflect.TypeOf(apiHealth{})},
}

func componentName(t reflect.Type) string {
	for _, c := range components {
		if c.t == t {
			return c.name
		}
	}
	return ""
}

func ref(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

// schemaOf describes a Go type as JSON Schema. Named component types become
// references, except at the top of their own component.
func schemaOf(t reflect.Type, top bool) map[string]any {
	if name := componentName(t); name != "" && !top {
		return ref(name)
	}
	switch t.Kind() {
	case reflect.Pointer:
		inner := schemaOf(t.Elem(), false)
		if kind, ok := inner["type"].(string); ok {
			inner["type"] = []string{kind, "null"}
			return inner
		}
		return map[string]any{"anyOf": []any{inner, map[string]any{"type": "null"}}}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), false)}
	case reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), false), "minItems": t.Len(), "maxItems": t.Len()}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), false)}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		properties := map[string]any{}
		required := []string{}
		var walk func(reflect.Type)
		walk = func(t reflect.Type) {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.Anonymous {
					walk(f.Type)
					continue
				}
				tag := f.Tag.Get("json")
				name, options, _ := strings.Cut(tag, ",")
				if name == "-" || !f.IsExported() {
					continue
				}
				if name == "" {
					name = f.Name
				}
				property := schemaOf(f.Type, false)
				if _, isRef := property["$ref"]; !isRef {
					if doc := f.Tag.Get("doc"); doc != "" {
						property["description"] = doc
					}
					if enum := f.Tag.Get("enum"); enum != "" {
						values := []any{}
						for _, v := range strings.Split(enum, ",") {
							values = append(values, v)
						}
						if f.Type.Kind() == reflect.Pointer {
							values = append(values, nil)
						}
						property["enum"] = values
					}
				}
				properties[name] = property
				if !strings.Contains(options, "omitempty") {
					required = append(required, name)
				}
			}
		}
		walk(t)
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	return map[string]any{}
}

func enumOf(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// publicationSchema is the create and replace body, from publicationFields.
func publicationSchema() map[string]any {
	properties := map[string]any{}
	for _, f := range publicationFields {
		p := map[string]any{"description": f.Description}
		switch f.Kind {
		case "boolean":
			p["type"] = "boolean"
		case "string":
			p["type"] = "string"
		case "strings":
			p["type"] = "array"
			p["items"] = map[string]any{"type": "string", "enum": enumOf(f.Allowed)}
		case "assets":
			p["type"] = "object"
			p["maxProperties"] = f.MaxItems
			p["propertyNames"] = map[string]any{"pattern": assetName.String()}
			p["additionalProperties"] = map[string]any{"type": "string", "contentEncoding": "base64", "x-max-bytes": f.MaxBytes}
		}
		if f.Kind == "string" && len(f.Allowed) > 0 {
			p["enum"] = enumOf(f.Allowed)
		}
		if f.Kind == "string" && f.MaxBytes > 0 {
			p["x-max-bytes"] = f.MaxBytes
			p["description"] = f.Description + " At most " + bytesLimit(f.MaxBytes) + "."
		}
		if f.MaxWords > 0 {
			p["x-max-words"] = f.MaxWords
		}
		if f.Name == "slug" {
			p["pattern"] = slugPattern.String()
			p["not"] = map[string]any{"enum": enumOf(reservedSlugs)}
		}
		properties[f.Name] = p
	}
	return map[string]any{
		"type":                 "object",
		"description":          "One page. Send exactly one of markdown or html. Every problem is reported at once, in errors.",
		"properties":           properties,
		"required":             []string{"title", "original_request"},
		"oneOf":                []any{map[string]any{"required": []string{"markdown"}}, map[string]any{"required": []string{"html"}}},
		"additionalProperties": false,
	}
}

func patchSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Send archived, title or both.",
		"properties": map[string]any{
			"archived": map[string]any{"type": "boolean", "description": "true hides the page in the archive; its links keep working. false restores it."},
			"title":    map[string]any{"type": "string", "x-max-bytes": maxTitleBytes, "description": "A new title. At most " + bytesLimit(maxTitleBytes) + "."},
		},
		"minProperties":        1,
		"additionalProperties": false,
	}
}

func jsonContent(schema map[string]any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}

func response(description string, schema map[string]any) map[string]any {
	r := map[string]any{"description": description}
	if schema != nil {
		r["content"] = jsonContent(schema)
	}
	return r
}

// errorResponses describes the failures an operation can answer with.
func errorResponses(responses map[string]any, statuses ...int) map[string]any {
	words := map[int]string{
		400: "validation_failed with every problem in errors, or invalid_json.",
		401: "authentication_required when the header is missing or malformed; invalid_credential when the key is wrong, rotated or revoked.",
		403: "browser_origin_forbidden for browser requests; forbidden when an agent key asks to list another byline.",
		404: "not_found: no page has that slug, or the page has another byline. An agent key gets the same answer for both.",
		409: "slug_conflict when the slug is taken, letter case aside; idempotency_key_in_progress while the first request with this key runs.",
		413: "payload_too_large: the request is over " + bytesLimit(maxRequestBytes) + ".",
		415: "unsupported_media_type: send Content-Type: application/json.",
		422: "idempotency_key_reused: the key was used for a different request.",
		500: "storage_unavailable or link_allocation_failed. Retryable.",
		503: "storage_cleanup_pending or public_sharing_unavailable. Retryable.",
	}
	for _, status := range statuses {
		responses[strconv.Itoa(status)] = response(words[status], ref("Error"))
	}
	return responses
}

func openAPIDocument() map[string]any {
	replacement := publicationSchema()
	delete(replacement["properties"].(map[string]any), "share_enabled")
	schemas := map[string]any{"Replacement": replacement, "Publication": publicationSchema(), "PageUpdate": patchSchema()}
	for _, c := range components {
		schemas[c.name] = schemaOf(c.t, true)
	}
	// Codes come from the lists the handlers use.
	setEnum := func(schema, property string, values []string) {
		schemas[schema].(map[string]any)["properties"].(map[string]any)[property].(map[string]any)["enum"] = enumOf(values)
	}
	setEnum("Error", "code", apiErrorCodes)
	setEnum("FieldError", "code", fieldErrorCodes)
	setEnum("Warning", "code", warningCodes)
	schemas["FieldError"].(map[string]any)["properties"].(map[string]any)["actual"] = map[string]any{"type": []string{"integer", "string"}, "description": "What was sent: a size in unit, or the value when it is short and safe to repeat."}
	schemas["Error"].(map[string]any)["properties"].(map[string]any)["errors"].(map[string]any)["description"] = "Every problem found, in field order. Fix them all before sending again."

	slug := map[string]any{"name": "slug", "in": "path", "required": true, "schema": map[string]any{"type": "string", "pattern": slugPattern.String()}, "description": "The page's slug, as in its url."}
	idempotency := map[string]any{"name": "Idempotency-Key", "in": "header", "required": false, "schema": map[string]any{"type": "string", "maxLength": maxIdempotencyKey, "pattern": "^[!-~]+$"}, "description": "Any unique string, such as a UUID. Sending the same request again with the same key within " + strconv.Itoa(int(idempotencyLifetime.Hours())) + " hours returns the first result, marked Idempotent-Replayed: true, and never saves twice. Keys are kept per credential."}
	created := response("The page was saved. Location holds its path.", ref("PageWrite"))
	created["headers"] = map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string"}, "description": "The page's path, such as /k3T."}}
	body := func(schema string) map[string]any {
		return map[string]any{"required": true, "content": jsonContent(ref(schema))}
	}
	example := examplePage()
	createBody := body("Publication")
	createBody["content"].(map[string]any)["application/json"].(map[string]any)["example"] = exampleRequest
	created["content"].(map[string]any)["application/json"].(map[string]any)["example"] = example
	// The owner's link length is a setting, not part of the contract.
	limits := apiLimits(0)
	delete(limits, "slug_chars")

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Cairn publishing API",
			"version": APIVersion,
			"description": "Publish pages to a private Cairn library and read them back. " +
				"Authenticate with Authorization: Bearer and an agent key from Settings, Authors. " +
				"An agent key signs pages with its own byline, and through this API lists, reads and changes only those pages; another author's page answers 404 like a missing one. " +
				"A key is a byline, not a lock: software that can reach the library's address can do anything the owner can. " +
				"The installation token reaches every page and must send agent. Only the owner deletes pages or changes existing sharing: " +
				"DELETE answers 405 method_not_allowed, so archive with PATCH instead. Browsers cannot use the API. " +
				"Every response is JSON. Failures share one shape: code, message, field, retryable and errors.",
		},
		"security": []any{map[string]any{"bearer": []string{}}},
		"paths": map[string]any{
			"/api": map[string]any{"get": map[string]any{
				"operationId": "index", "summary": "A short index of the API with one worked example.", "security": []any{},
				"responses": map[string]any{"200": response("The index.", map[string]any{"type": "object"})},
			}},
			"/api/openapi.json": map[string]any{"get": map[string]any{
				"operationId": "openapi", "summary": "This document.", "security": []any{},
				"responses": map[string]any{"200": response("This document.", map[string]any{"type": "object"})},
			}},
			"/api/files":        map[string]any{"post": fileOperation("Publish a private file page.", "publishFile", 201)},
			"/api/files/{slug}": map[string]any{"parameters": []any{slug}, "put": fileOperation("Replace a page with a file; sharing and passwords stay.", "replaceFile", 200)},
			"/api/pages": map[string]any{
				"get": map[string]any{
					"operationId": "listPages",
					"summary":     "List pages newest first, or search them.",
					"description": "q finds pages by their title, request, byline and words, the way the library's search does: every word must match, each as the start of a word, \"quoted words\" as a phrase, ignoring case and accents. Results come best match first. An agent key lists only its own pages.",
					"parameters": []any{
						map[string]any{"name": "q", "in": "query", "schema": map[string]any{"type": "string", "maxLength": 200}, "description": "Search words."},
						map[string]any{"name": "agent", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Only pages with this byline. An agent key may only name its own."},
						map[string]any{"name": "archived", "in": "query", "schema": map[string]any{"type": "string", "enum": []any{"true", "false"}}, "description": "true lists the archive instead. Default false."},
						map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": maxListLimit}, "description": "Pages per response. Default " + strconv.Itoa(defaultListLimit) + "."},
						map[string]any{"name": "cursor", "in": "query", "schema": map[string]any{"type": "string"}, "description": "next_cursor from the previous response."},
					},
					"responses": errorResponses(map[string]any{"200": response("One page of results.", ref("PageList"))}, 400, 401, 403, 500),
				},
				"post": map[string]any{
					"operationId": "publishPage",
					"summary":     "Publish a private page.",
					"parameters":  []any{idempotency},
					"requestBody": createBody,
					"responses":   errorResponses(map[string]any{"201": created}, 400, 401, 403, 409, 413, 415, 422, 500, 503),
				},
			},
			"/api/pages/{slug}": map[string]any{
				"parameters": []any{slug},
				"get": map[string]any{
					"operationId": "readPage",
					"summary":     "Read a page, its request and the Markdown or HTML Cairn kept.",
					"responses":   errorResponses(map[string]any{"200": response("The page.", ref("PageRead"))}, 401, 403, 404, 500),
				},
				"head": map[string]any{
					"operationId": "pageExists",
					"summary":     "Check that a page exists and your key reaches it.",
					"responses":   map[string]any{"200": response("It exists and your key reaches it.", nil), "401": response("No key, or not a valid one.", nil), "404": response("No page has that slug, or it has another byline.", nil)},
				},
				"put": map[string]any{
					"operationId": "replacePage",
					"summary":     "Replace a page completely.",
					"description": "Send the whole page again, with every image it still needs. The slug, date and sharing stay; leaving out request_summary or request_source keeps the saved ones.",
					"parameters":  []any{idempotency},
					"requestBody": body("Replacement"),
					"responses":   errorResponses(map[string]any{"200": response("The page was replaced.", ref("PageWrite"))}, 400, 401, 403, 404, 409, 413, 415, 422, 500, 503),
				},
				"patch": map[string]any{
					"operationId": "updatePage",
					"summary":     "Archive or restore a page, or change its title.",
					"parameters":  []any{idempotency},
					"requestBody": body("PageUpdate"),
					"responses":   errorResponses(map[string]any{"200": response("The page was changed.", ref("PageWrite"))}, 400, 401, 403, 404, 409, 415, 422, 500),
				},
			},
			"/healthz": map[string]any{"get": map[string]any{
				"operationId": "health", "summary": "Whether Cairn is up. The public listener answers with status alone.", "security": []any{},
				"responses": map[string]any{"200": response("Cairn is up.", ref("Health")), "503": response("The database or storage is unavailable.", ref("Health"))},
			}},
		},
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{"bearer": map[string]any{
				"type": "http", "scheme": "bearer",
				"description": "An agent key (cairn_ followed by 43 characters) from Settings, Authors, or the installation token.",
			}},
		},
		"x-limits": limits,
	}
}

// fileOperation describes the streamed multipart API, not JSON publication.
func fileOperation(summary, id string, status int) map[string]any {
	fields := map[string]any{"file": map[string]any{"type": "string", "format": "binary", "description": "One inert file, up to 512 MiB.", "x-max-bytes": maxFileBytes}}
	for _, f := range publicationFields {
		switch f.Name {
		case "title", "agent", "original_request", "request_summary", "request_source", "slug":
			fields[f.Name] = map[string]any{"type": "string", "description": f.Description}
		}
	}
	return map[string]any{"operationId": id, "summary": summary, "requestBody": map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": map[string]any{"type": "object", "properties": fields, "required": []string{"file", "original_request"}, "additionalProperties": false}}}}, "responses": errorResponses(map[string]any{strconv.Itoa(status): response("The file page was saved.", ref("PageWrite"))}, 400, 401, 403, 404, 409, 413, 415, 500, 503)}
}
