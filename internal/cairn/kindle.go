package cairn

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// MailConfig is the SMTP account that sends pages to the owner's Kindle.
// An empty Server turns Send to Kindle off. Amazon only accepts mail from
// addresses on the owner's Approved Personal Document E-mail List.
type MailConfig struct {
	Server   string // host:port; port 465 uses implicit TLS, others STARTTLS
	Username string // defaults to From
	Password string
	From     string
}

func (m MailConfig) enabled() bool { return m.Server != "" }

func (m *MailConfig) validate() error {
	if *m == (MailConfig{}) {
		return nil
	}
	host, port, err := net.SplitHostPort(m.Server)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("mail server must be host:port, for example smtp.gmail.com:587")
	}
	if !bareAddress(m.From) {
		return fmt.Errorf("mail from must be a plain email address, for example you@example.com")
	}
	if m.Username == "" {
		m.Username = m.From
	}
	if m.Password == "" {
		return fmt.Errorf("mail password is required when a mail server is set")
	}
	return nil
}

// bareAddress accepts one address with no display name or header breaks.
func bareAddress(s string) bool {
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n<>, ") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

func (s *Server) kindleAddress() string {
	var address string
	_ = s.db.QueryRow(`SELECT address FROM kindle_preferences WHERE id=1`).Scan(&address)
	return address
}

// Gmail rejects messages over 25 MB after base64 adds a third.
const kindleBookLimit = 18 << 20

// kindleNotices words each Send to Kindle result. kindleNotice (feedback.go)
// adds the next step to the failures.
var kindleNotices = map[string]string{
	"sent":        "Sent to your Kindle. It usually arrives within a few minutes.",
	"unreachable": "Cairn couldn’t reach the mail server, so nothing was sent.",
	"signin":      "The mail server refused Cairn’s sign-in, so nothing was sent. The mail account’s name or password on the server needs checking.",
	"refused":     "The mail server refused the message, so nothing was sent. Check your Kindle address in Settings.",
	"busy":        "The mail server asked Cairn to try later, so nothing was sent.",
	"failed":      "Cairn couldn’t send this page to your Kindle. The server’s log says why.",
	"too-large":   "This page is too large to email to a Kindle, which takes about 18 MB at most.",
	"expired":     "That button had expired, so nothing was sent.",
}

// kindleRetries are the failures that sending again can fix: the network or
// the mail server was out for a moment. A refused sign-in or message, or a
// page that is too large, fails the same way every time.
var kindleRetries = map[string]bool{"unreachable": true, "busy": true, "failed": true}

// mailFailure says at which step a send failed, so the owner hears the cause
// in plain words. The log keeps the mail server's own message.
type mailFailure struct {
	kind string
	err  error
}

func (e *mailFailure) Error() string { return e.err.Error() }
func (e *mailFailure) Unwrap() error { return e.err }

// mailStep labels an error from one step of the conversation. A broken
// connection at any step means the server could not be reached; a reply
// refuses the sign-in, the message for good (5xx) or for now (4xx).
func mailStep(step string, err error) error {
	if err == nil {
		return nil
	}
	var reply *textproto.Error
	var network net.Error
	switch {
	case errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 500:
		return &mailFailure{"busy", err}
	case errors.As(err, &reply) && step == "signin":
		return &mailFailure{"signin", err}
	case errors.As(err, &reply):
		return &mailFailure{"refused", err}
	case errors.As(err, &network), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return &mailFailure{"unreachable", err}
	}
	return err
}

// mailResult is the notice a failed send gets: its cause when Cairn knows
// it, otherwise the plain failure.
func mailResult(err error) string {
	var failure *mailFailure
	if errors.As(err, &failure) {
		return failure.kind
	}
	return "failed"
}

// kindleReturn is where a send goes back to: the reader, or the library with
// the page named so the result shows beside its row.
func kindleReturn(r *http.Request, slug, result string) string {
	if r.FormValue("return") == "reader" {
		return "/" + slug + "?kindle=" + result
	}
	return "/?kindle=" + result + "&page=" + url.QueryEscape(slug)
}

// sendToKindle runs outside the write lock: SMTP can take seconds.
func (s *Server) sendToKindle(w http.ResponseWriter, r *http.Request, slug string) {
	if !s.cfg.Mail.enabled() {
		s.showError(w, http.StatusServiceUnavailable, problemNoMail)
		return
	}
	address := s.kindleAddress()
	if address == "" {
		http.Redirect(w, r, "/settings?kindle=missing#kindle", http.StatusSeeOther)
		return
	}
	s.mu.RLock()
	p, err := s.getPage(slug)
	var book []byte
	if err == nil {
		book, err = s.pageBook(p)
	}
	s.mu.RUnlock()
	if err == sql.ErrNoRows {
		s.notFound(w, r)
		return
	}
	notice := "sent"
	if err != nil {
		log.Printf("Cairn: Send to Kindle could not build %s: %v", slug, err)
		notice = "failed"
	} else if len(book) > kindleBookLimit {
		notice = "too-large"
	} else if err = smtpSend(s.cfg.Mail, address, kindleMessage(s.cfg.Mail.From, address, p, book, time.Now())); err != nil {
		notice = mailResult(err)
		log.Printf("Cairn: Send to Kindle failed for %s (%s): %v", slug, notice, err)
	}
	http.Redirect(w, r, kindleReturn(r, slug, notice), http.StatusSeeOther)
}

func (s *Server) pageBook(p savedPage) ([]byte, error) {
	dir := filepath.Join(s.cfg.DataDir, "objects", p.Object)
	page, err := os.ReadFile(filepath.Join(dir, "page.html"))
	if err != nil {
		return nil, err
	}
	assets := map[string][]byte{}
	entries, err := os.ReadDir(filepath.Join(dir, "assets"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !assetName.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "assets", entry.Name()))
		if err != nil {
			return nil, err
		}
		assets[entry.Name()] = data
	}
	return buildBook(p, string(page), assets)
}

// Kindle renders EPUB 3 with images. Scripts, forms and embedded frames
// cannot run there, so the book keeps the report's text, tables and images.
var bookDropped = map[string]bool{"head": true, "script": true, "style": true, "noscript": true, "template": true, "iframe": true, "frame": true, "frameset": true, "object": true, "embed": true, "canvas": true, "video": true, "audio": true, "source": true, "track": true, "link": true, "meta": true, "base": true, "dialog": true, "map": true, "area": true, "input": true, "button": true, "select": true, "textarea": true, "slot": true, "portal": true, "xmp": true, "plaintext": true, "noembed": true, "noframes": true, "foreignObject": true, "foreignobject": true}
var bookAttributes = map[string]bool{"id": true, "class": true, "lang": true, "dir": true, "title": true, "href": true, "src": true, "alt": true, "width": true, "height": true, "colspan": true, "rowspan": true, "scope": true, "headers": true, "start": true, "reversed": true, "type": true, "datetime": true, "cite": true, "abbr": true, "role": true, "open": true}
var xmlName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
var bookImageTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}

type bookImage struct{ name, media string }

type bookBuilder struct {
	slug      string
	assets    map[string][]byte
	files     map[string][]byte
	images    []bookImage
	byAsset   map[string]string
	ids       map[string]bool
	svg, math bool
}

func buildBook(p savedPage, page string, assets map[string][]byte) ([]byte, error) {
	page, headings := reportHeadings(page)
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, err
	}
	b := &bookBuilder{slug: p.Slug, assets: assets, files: map[string][]byte{}, byAsset: map[string]string{}, ids: map[string]bool{}}
	lang := "en"
	var body *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "html" {
			for _, a := range n.Attr {
				if a.Key == "lang" && xmlName.MatchString(a.Val) {
					lang = a.Val
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "body" && body == nil {
			body = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if body == nil {
		return nil, fmt.Errorf("page has no body")
	}
	b.clean(body)
	// The byline sits under the report's own title, or under one Cairn adds.
	byline := &html.Node{Type: html.ElementNode, Data: "p", Attr: []html.Attribute{{Key: "class", Val: "byline"}}}
	byline.AppendChild(&html.Node{Type: html.TextNode, Data: "Prepared by " + p.Agent + bookDate(p.CreatedAt, " · ")})
	if h1 := firstElement(body, "h1"); h1 != nil {
		h1.Parent.InsertBefore(byline, h1.NextSibling)
	} else {
		heading := &html.Node{Type: html.ElementNode, Data: "h1"}
		heading.AppendChild(&html.Node{Type: html.TextNode, Data: p.Title})
		body.InsertBefore(heading, body.FirstChild)
		body.InsertBefore(byline, heading.NextSibling)
	}
	var content bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&content, c); err != nil {
			return nil, err
		}
	}
	title := html.EscapeString(p.Title)
	properties := ""
	if b.svg {
		properties = "svg"
	}
	if b.math {
		properties = strings.TrimSpace(properties + " mathml")
	}
	if properties != "" {
		properties = ` properties="` + properties + `"`
	}
	xhtml := `<?xml version="1.0" encoding="utf-8"?>` + "\n<!DOCTYPE html>\n" + `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="` + lang + `" xml:lang="` + lang + `"><head><meta charset="utf-8"/><title>` + title + `</title><link rel="stylesheet" type="text/css" href="style.css"/></head><body>` + content.String() + "</body></html>\n"

	var nav strings.Builder
	var writeNav func([]*contentsEntry)
	writeNav = func(entries []*contentsEntry) {
		for _, e := range entries {
			nav.WriteString(`<li><a href="page.xhtml#` + html.EscapeString(e.ID) + `">` + html.EscapeString(e.Text) + "</a>")
			if len(e.Children) > 0 {
				nav.WriteString("<ol>")
				writeNav(e.Children)
				nav.WriteString("</ol>")
			}
			nav.WriteString("</li>")
		}
	}
	nav.WriteString(`<li><a href="page.xhtml">` + title + "</a>")
	if entries := b.present(reportContents(headings, p.Slug)); len(entries) > 0 {
		nav.WriteString("<ol>")
		writeNav(entries)
		nav.WriteString("</ol>")
	}
	nav.WriteString("</li>")
	navDoc := `<?xml version="1.0" encoding="utf-8"?>` + "\n<!DOCTYPE html>\n" + `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="` + lang + `" xml:lang="` + lang + `"><head><meta charset="utf-8"/><title>` + title + `</title></head><body><nav epub:type="toc" id="toc"><h1>Contents</h1><ol>` + nav.String() + "</ol></nav></body></html>\n"

	var manifest strings.Builder
	for i, img := range b.images {
		manifest.WriteString(`<item id="img` + strconv.Itoa(i+1) + `" href="images/` + img.name + `" media-type="` + img.media + `"/>`)
	}
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	opf := `<?xml version="1.0" encoding="utf-8"?>` + "\n" + `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="` + lang + `"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">urn:cairn:` + html.EscapeString(p.ID) + `</dc:identifier><dc:title>` + title + `</dc:title><dc:language>` + lang + `</dc:language><dc:creator>` + html.EscapeString(p.Agent) + `</dc:creator>` + bookDate(p.CreatedAt, "<dc:date>") + `<meta property="dcterms:modified">` + modified + `</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="page" href="page.xhtml" media-type="application/xhtml+xml"` + properties + `/><item id="css" href="style.css" media-type="text/css"/>` + manifest.String() + `</manifest><spine><itemref idref="page"/></spine></package>` + "\n"

	var out bytes.Buffer
	z := zip.NewWriter(&out)
	// The mimetype entry must come first and stay uncompressed.
	f, err := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return nil, err
	}
	io.WriteString(f, "application/epub+zip")
	files := []struct {
		name string
		data []byte
	}{
		{"META-INF/container.xml", []byte(`<?xml version="1.0" encoding="utf-8"?>` + "\n" + `<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>` + "\n")},
		{"OEBPS/content.opf", []byte(opf)},
		{"OEBPS/nav.xhtml", []byte(navDoc)},
		{"OEBPS/page.xhtml", []byte(xhtml)},
		{"OEBPS/style.css", []byte(bookCSS)},
	}
	for _, img := range b.images {
		files = append(files, struct {
			name string
			data []byte
		}{"OEBPS/images/" + img.name, b.files[img.name]})
	}
	for _, file := range files {
		f, err := z.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

const bookCSS = `body{line-height:1.5}
h1,h2,h3,h4{line-height:1.25;page-break-after:avoid}
.byline{color:#555;font-size:.9em;margin-bottom:1.5em}
img{max-width:100%;height:auto}
figure{margin:1em 0}
figcaption{font-size:.9em;color:#555}
blockquote{margin:1em 0 1em 1em;padding-left:1em;border-left:2px solid #999}
pre{white-space:pre-wrap;font-size:.85em}
table{border-collapse:collapse;margin:1em 0}
th,td{border:1px solid #999;padding:.3em .5em;text-align:left;vertical-align:top}
`

func bookDate(created, prefix string) string {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return ""
	}
	if prefix == "<dc:date>" {
		return "<dc:date>" + t.UTC().Format("2006-01-02T15:04:05Z") + "</dc:date>"
	}
	return prefix + t.Format("2 Jan 2006")
}

// clean rewrites the report body into XHTML that Kindle accepts.
func (b *bookBuilder) clean(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode || c.Type == html.DoctypeNode:
			n.RemoveChild(c)
		case c.Type == html.TextNode:
			c.Data = strings.Map(xmlRune, c.Data)
		case c.Type != html.ElementNode:
		case bookDropped[c.Data] || hasAttr(c, "hidden"):
			n.RemoveChild(c)
		case c.Namespace == "" && (strings.Contains(c.Data, "-") || c.Data == "form" || c.Data == "picture"):
			// Custom elements and wrappers keep their children.
			b.clean(c)
			for gc := c.FirstChild; gc != nil; {
				following := gc.NextSibling
				c.RemoveChild(gc)
				n.InsertBefore(gc, c)
				gc = following
			}
			n.RemoveChild(c)
		case c.Data == "img" && c.Namespace == "":
			if !b.image(c) {
				if alt := attr(c, "alt"); alt != "" {
					n.InsertBefore(&html.Node{Type: html.TextNode, Data: alt}, c)
				}
				n.RemoveChild(c)
			}
		default:
			b.attributes(c)
			b.clean(c)
		}
		c = next
	}
}

func (b *bookBuilder) attributes(n *html.Node) {
	foreign := n.Namespace == "svg" || n.Namespace == "math"
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		switch {
		case strings.HasPrefix(key, "on") || key == "style":
			continue
		case foreign && a.Namespace == "xlink" && key == "href":
			a.Namespace = ""
			if !strings.HasPrefix(a.Val, "#") {
				continue
			}
		case foreign && a.Namespace == "" && xmlName.MatchString(a.Key):
		case !foreign && a.Namespace == "" && (bookAttributes[key] || strings.HasPrefix(key, "aria-")) && xmlName.MatchString(key):
			a.Key = key
		default:
			continue
		}
		if key == "href" && !foreign && !(strings.HasPrefix(a.Val, "#") || strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "mailto:")) {
			continue
		}
		if key == "id" {
			b.ids[a.Val] = true
		}
		kept = append(kept, a)
	}
	n.Attr = kept
	if n.Data == "svg" && n.Namespace == "svg" {
		b.svg = true
		n.Attr = append([]html.Attribute{{Key: "xmlns", Val: "http://www.w3.org/2000/svg"}}, withoutAttr(n.Attr, "xmlns")...)
	}
	if n.Data == "math" && n.Namespace == "math" {
		b.math = true
		n.Attr = append([]html.Attribute{{Key: "xmlns", Val: "http://www.w3.org/1998/Math/MathML"}}, withoutAttr(n.Attr, "xmlns")...)
	}
}

// image points an img at a file inside the book, or reports that it cannot.
func (b *bookBuilder) image(n *html.Node) bool {
	src := attr(n, "src")
	var name string
	var data []byte
	switch {
	case strings.HasPrefix(src, "/_assets/"+b.slug+"/"):
		name = strings.TrimPrefix(src, "/_assets/"+b.slug+"/")
	case strings.HasPrefix(src, "/_content/"+b.slug+"?asset="):
		name = strings.TrimPrefix(src, "/_content/"+b.slug+"?asset=")
	case strings.HasPrefix(src, "?asset="):
		name = strings.TrimPrefix(src, "?asset=")
	case strings.HasPrefix(src, "data:image/"):
		meta, payload, ok := strings.Cut(strings.TrimPrefix(src, "data:"), ",")
		if !ok || !strings.HasSuffix(meta, ";base64") {
			return false
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(payload), ""))
		if err != nil {
			return false
		}
		data = decoded
	default:
		return false
	}
	if name != "" {
		if !assetName.MatchString(name) {
			return false
		}
		if file, ok := b.byAsset[name]; ok {
			b.pointAt(n, file)
			return true
		}
		data = b.assets[name]
	}
	media := http.DetectContentType(data)
	ext, ok := bookImageTypes[media]
	if len(data) == 0 || !ok {
		return false
	}
	file := "image-" + strconv.Itoa(len(b.images)+1) + ext
	b.files[file] = data
	b.images = append(b.images, bookImage{file, media})
	if name != "" {
		b.byAsset[name] = file
	}
	b.pointAt(n, file)
	return true
}

func (b *bookBuilder) pointAt(n *html.Node, file string) {
	alt := attr(n, "alt")
	n.Attr = []html.Attribute{{Key: "src", Val: "images/" + file}, {Key: "alt", Val: alt}}
}

// present drops contents entries whose heading did not survive cleaning.
func (b *bookBuilder) present(entries []*contentsEntry) []*contentsEntry {
	var kept []*contentsEntry
	for _, e := range entries {
		e.Children = b.present(e.Children)
		if b.ids[e.ID] {
			kept = append(kept, e)
		} else {
			kept = append(kept, e.Children...)
		}
	}
	return kept
}

// xmlRune drops control characters that XML 1.0 forbids.
func xmlRune(r rune) rune {
	if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF) {
		return r
	}
	return -1
}

func firstElement(n *html.Node, name string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == name && c.Namespace == "" {
			return c
		}
		if found := firstElement(c, name); found != nil {
			return found
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return true
		}
	}
	return false
}
func withoutAttr(attrs []html.Attribute, key string) []html.Attribute {
	var kept []html.Attribute
	for _, a := range attrs {
		if a.Key != key {
			kept = append(kept, a)
		}
	}
	return kept
}

var unsafeFileName = regexp.MustCompile(`[^A-Za-z0-9 _-]+`)

func kindleMessage(from, to string, p savedPage, book []byte, now time.Time) []byte {
	name := strings.Join(strings.Fields(unsafeFileName.ReplaceAllString(p.Title, " ")), " ")
	if len(name) > 80 {
		name = strings.TrimSpace(name[:80])
	}
	if name == "" {
		name = p.Slug
	}
	var msg bytes.Buffer
	parts := multipart.NewWriter(&msg)
	domain := from[strings.LastIndex(from, "@")+1:]
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n", from, to, mime.QEncoding.Encode("utf-8", p.Title), now.Format(time.RFC1123Z), randomID(), domain, parts.Boundary())
	text, _ := parts.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"7bit"}})
	io.WriteString(text, "Sent from Cairn.\r\n")
	attachment, _ := parts.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType("application/epub+zip", map[string]string{"name": name + ".epub"})},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name + ".epub"})},
		"Content-Transfer-Encoding": {"base64"},
	})
	encoded := base64.StdEncoding.EncodeToString(book)
	for len(encoded) > 76 {
		io.WriteString(attachment, encoded[:76]+"\r\n")
		encoded = encoded[76:]
	}
	io.WriteString(attachment, encoded+"\r\n")
	parts.Close()
	return msg.Bytes()
}

// smtpSend requires TLS except on a loopback relay.
func smtpSend(cfg MailConfig, to string, msg []byte) error {
	host, port, err := net.SplitHostPort(cfg.Server)
	if err != nil {
		return err
	}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", cfg.Server, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", cfg.Server)
	}
	if err != nil {
		return mailStep("connect", err)
	}
	conn.SetDeadline(time.Now().Add(25 * time.Second))
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return mailStep("connect", err)
	}
	defer c.Close()
	if port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return mailStep("connect", err)
			}
		} else if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("mail server %s does not offer STARTTLS", host)
		}
	}
	if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host)); err != nil {
		return mailStep("signin", fmt.Errorf("mail sign-in failed: %w", err))
	}
	if err := c.Mail(cfg.From); err != nil {
		return mailStep("message", err)
	}
	if err := c.Rcpt(to); err != nil {
		return mailStep("message", err)
	}
	data, err := c.Data()
	if err != nil {
		return mailStep("message", err)
	}
	if _, err := data.Write(msg); err != nil {
		return mailStep("message", err)
	}
	if err := data.Close(); err != nil {
		return mailStep("message", err)
	}
	// The server took the message when DATA closed; a goodbye that goes
	// wrong after that does not unsend it.
	_ = c.Quit()
	return nil
}
