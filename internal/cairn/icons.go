package cairn

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
)

// App and browser icons, drawn once by scripts/generate-icons.go. They are
// served from hashed /_ui/ addresses like the stylesheet, and also at the two
// fixed addresses browsers ask for without being told.

//go:embed ui/icon-32.png
var icon32 []byte

//go:embed ui/icon-180.png
var icon180 []byte

//go:embed ui/icon-192.png
var icon192 []byte

//go:embed ui/icon-512.png
var icon512 []byte

// iconURL maps an icon's size to its published /_ui/ address.
var iconURL = map[int]string{}

// manifestJSON is the private library's web app manifest. Only the private
// listener serves it, so a shared page never offers to install itself.
var manifestJSON []byte

func init() {
	for size, data := range map[int][]byte{32: icon32, 180: icon180, 192: icon192, 512: icon512} {
		published := hashedName("icon-"+strconv.Itoa(size)+".png", data)
		uiAssets[published] = &uiAsset{body: data, contentType: "image/png"}
		iconURL[size] = "/_ui/" + published
	}
	type manifestIcon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	}
	manifestJSON, _ = json.Marshal(map[string]any{
		"id":               "/",
		"name":             "Cairn",
		"short_name":       "Cairn",
		"description":      "Your private library of pages, files and links.",
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#eef2f5",
		"theme_color":      "#29485f",
		"icons": []manifestIcon{
			{iconURL[192], "192x192", "image/png", "any"},
			{iconURL[512], "512x512", "image/png", "any"},
			{iconURL[512], "512x512", "image/png", "maskable"},
		},
	})
}

// serveFixedIcon answers /favicon.ico and /apple-touch-icon.png on both
// listeners. It reports whether the path was one of them.
func serveFixedIcon(w http.ResponseWriter, r *http.Request) bool {
	var data []byte
	switch r.URL.Path {
	case "/favicon.ico":
		data = icon32
	case "/apple-touch-icon.png", "/apple-touch-icon-precomposed.png":
		data = icon180
	default:
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", "public, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		w.Write(data)
	}
	return true
}

// serveManifest answers /manifest.webmanifest on the private listener.
func serveManifest(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/manifest.webmanifest" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", "application/manifest+json")
	h.Set("Cache-Control", "no-cache")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Length", strconv.Itoa(len(manifestJSON)))
	if r.Method != http.MethodHead {
		w.Write(manifestJSON)
	}
	return true
}

// iconFor is the template function that names an icon's address.
func iconFor(size int) string {
	url, ok := iconURL[size]
	if !ok {
		panic("cairn: no icon of size " + strconv.Itoa(size))
	}
	return url
}

// isOwnerPage reports whether a page's data is an owner page, which embeds
// ownerChrome. Visitor pages on the public listener don't.
func isOwnerPage(data any) bool {
	_, ok := data.(interface{ shortcutsOff() bool })
	return ok
}
