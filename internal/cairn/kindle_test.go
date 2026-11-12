package cairn_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/rowesk/cairn/internal/cairn"
)

// fakeSMTP accepts one plain-text session per connection on loopback.
type fakeSMTP struct {
	addr     string
	password string
	mu       sync.Mutex
	from, to string
	auth     string
	data     []byte
}

func newFakeSMTP(t *testing.T, password string) *fakeSMTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	f := &fakeSMTP{addr: l.Addr().String(), password: password}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	text := textproto.NewConn(conn)
	text.PrintfLine("220 fake ready")
	for {
		line, err := text.ReadLine()
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		switch verb {
		case "EHLO":
			text.PrintfLine("250-fake\r\n250 AUTH PLAIN")
		case "AUTH":
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) != 3 || parts[2] != f.password {
				text.PrintfLine("535 bad credentials")
				continue
			}
			f.mu.Lock()
			f.auth = parts[1]
			f.mu.Unlock()
			text.PrintfLine("235 ok")
		case "MAIL":
			f.mu.Lock()
			f.from = line
			f.mu.Unlock()
			text.PrintfLine("250 ok")
		case "RCPT":
			f.mu.Lock()
			f.to = line
			f.mu.Unlock()
			text.PrintfLine("250 ok")
		case "DATA":
			text.PrintfLine("354 go")
			data, err := text.ReadDotBytes()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = data
			f.mu.Unlock()
			text.PrintfLine("250 queued")
		case "QUIT":
			text.PrintfLine("221 bye")
			return
		default:
			text.PrintfLine("502 unknown")
		}
	}
}

func (f *fakeSMTP) message() (from, to, auth string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.from, f.to, f.auth, f.data
}

func kindlePost(t *testing.T, srv *httptest.Server, slug string, values url.Values) (int, string) {
	t.Helper()
	_, _, body := request(t, srv, "GET", "/settings", nil, "")
	values.Set("csrf", string(regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)[1]))
	req, _ := http.NewRequest("POST", srv.URL+"/manage/"+slug, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	return res.StatusCode, res.Header.Get("Location")
}

const onePixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="

func TestSendToKindleIsOffWithoutMailServer(t *testing.T) {
	srv, close := start(t, t.TempDir())
	defer close()
	if status, _, body := request(t, srv, "POST", "/api/pages", page("Bulb comparison"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	_, _, body := request(t, srv, "GET", "/", nil, "")
	if strings.Contains(string(body), "Send to Kindle") {
		t.Error("library offers Kindle without a mail server")
	}
	_, _, body = request(t, srv, "GET", "/settings", nil, "")
	if !strings.Contains(string(body), "Sending needs a mail account on the server first.") || !regexp.MustCompile(`id="kindle-address"[^>]* disabled`).MatchString(string(body)) || strings.Contains(string(body), "Save Kindle address") {
		t.Error("settings hides that sending is off, or offers a field that can't work")
	}
	if status, _ := kindlePost(t, srv, "report", url.Values{"action": {"kindle"}}); status != 503 {
		t.Fatal(status)
	}
}

func TestSendToKindleEmailsAnEPUB(t *testing.T) {
	smtp := newFakeSMTP(t, "app-password")
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, PublicBaseURL: "https://reports.example.com", Mail: cairn.MailConfig{Server: smtp.addr, Password: "app-password", From: "owner@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app)
	defer srv.Close()
	pub := httptest.NewServer(app.PublicHandler())
	defer pub.Close()

	in := page("Bulbs & lamps")
	in["html"] = `<!doctype html><html lang="en-GB"><head><style>body{color:red}</style><script>alert(1)</script></head><body><h1>Bulbs &amp; lamps</h1><p onclick="x()" style="color:red">Warm <b>light</b>&nbsp;wins<br>today.</p><h2>Colour</h2><img src="?asset=bulb.png" alt="Bulb"><img src="/_assets/report/bulb.png" alt="Same bulb"><img src="data:image/png;base64,` + onePixel + `" alt="Inline"><img src="https://example.com/remote.png" alt="Remote picture"><h2>Price</h2><table><tr><th>Bulb</th><td>£4</td></tr></table><svg viewBox="0 0 10 10"><use xlink:href="#dot"/><circle id="dot" r="2"/></svg><h2>Verdict</h2><my-widget><p>Kept text</p></my-widget><iframe src="https://example.com"></iframe><form><input name="q"><button>Go</button></form><a href="/elsewhere">Private link</a> <a href="https://example.com/x?a=1&amp;b=2">Public link</a><p hidden>Hidden note</p></body></html>`
	in["assets"] = map[string]string{"bulb.png": onePixel}
	if status, _, body := request(t, srv, "POST", "/api/pages", in, token); status != 201 {
		t.Fatal(status, string(body))
	}

	_, _, body := request(t, srv, "GET", "/", nil, "")
	if !strings.Contains(string(body), "Add your Kindle address first") {
		t.Error("library does not ask for a Kindle address")
	}
	if status, location := kindlePost(t, srv, "report", url.Values{"action": {"kindle"}}); status != 303 || location != "/settings?kindle=missing#kindle" {
		t.Fatal(status, location)
	}
	if code, _, _ := authorForm(t, srv, "/settings", url.Values{"action": {"kindle"}, "kindle_address": {"me@kindle.com\r\nBcc: x@example.com"}}, srv.URL); code != 400 {
		t.Fatal("header injection accepted", code)
	}
	if code, _, _ := authorForm(t, srv, "/settings", url.Values{"action": {"kindle"}, "kindle_address": {"me@kindle.com"}}, srv.URL); code != 303 {
		t.Fatal(code)
	}
	_, _, body = request(t, srv, "GET", "/settings", nil, "")
	if !strings.Contains(string(body), `value="me@kindle.com"`) || !strings.Contains(string(body), "owner@example.com") {
		t.Error("settings lost the Kindle address or sender")
	}
	_, _, body = request(t, srv, "GET", "/", nil, "")
	if !strings.Contains(string(body), `name="action" value="kindle"`) {
		t.Error("library menu has no Send to Kindle form")
	}
	_, headers, body := request(t, srv, "GET", "/report", nil, "")
	if !strings.Contains(string(body), `<button class="menu-kindle">`) || !strings.Contains(string(body), `<input type="hidden" name="return" value="reader">`) || !strings.Contains(headers.Get("Content-Security-Policy"), "form-action 'self'") {
		t.Error("reader has no Send to Kindle form")
	}
	if status := ownerPost(t, srv, "report", url.Values{"action": {"share"}}); status != 303 {
		t.Fatal(status)
	}
	_, headers, body = request(t, pub, "GET", "/report", nil, "")
	if strings.Contains(string(body), "Kindle") || strings.Contains(string(body), `name="csrf"`) || !strings.Contains(headers.Get("Content-Security-Policy"), "form-action 'none'") {
		t.Error("public reader exposes owner controls")
	}
	if res, err := pub.Client().PostForm(pub.URL+"/manage/report", url.Values{"action": {"kindle"}}); err != nil || res.StatusCode != 404 {
		t.Error("public listener accepted a Kindle request", err)
	} else {
		res.Body.Close()
	}

	status, location := kindlePost(t, srv, "report", url.Values{"action": {"kindle"}, "return": {"reader"}})
	if status != 303 || location != "/report?kindle=sent" {
		t.Fatal(status, location)
	}
	_, _, body = request(t, srv, "GET", location, nil, "")
	if !strings.Contains(string(body), "Sent to your Kindle") {
		t.Error("reader does not confirm the send")
	}
	from, to, auth, data := smtp.message()
	if from != "MAIL FROM:<owner@example.com>" || !strings.HasPrefix(to, "RCPT TO:<me@kindle.com>") || auth != "owner@example.com" {
		t.Fatal(from, to, auth)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); subject != "Bulbs & lamps" {
		t.Error("subject", subject)
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	parts := multipart.NewReader(msg.Body, params["boundary"])
	var book []byte
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if part.FileName() == "Bulbs lamps.epub" {
			book, _ = io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		}
	}
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal("attachment is not an EPUB zip:", err)
	}
	if z.File[0].Name != "mimetype" || z.File[0].Method != zip.Store {
		t.Error("mimetype must be the first, stored entry")
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		files[f.Name] = string(b)
	}
	for _, name := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/page.xhtml"} {
		decoder := xml.NewDecoder(strings.NewReader(files[name]))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed XML: %v\n%s", name, err, files[name])
			}
		}
	}
	xhtml := files["OEBPS/page.xhtml"]
	for _, gone := range []string{"<script", "alert(1)", "color:red", "onclick", "<iframe", "<input", "<button", "Hidden note", "remote.png", "my-widget", `href="/elsewhere"`, "PRIVATE"} {
		if strings.Contains(xhtml, gone) {
			t.Errorf("book kept %q", gone)
		}
	}
	for _, kept := range []string{`lang="en-GB"`, "Prepared by ExampleAgent", `src="images/image-1.png"`, `src="images/image-2.png"`, "Remote picture", "Kept text", "£4", `<svg xmlns="http://www.w3.org/2000/svg"`, `href="#dot"`, `href="https://example.com/x?a=1&amp;b=2"`, "<br/>"} {
		if !strings.Contains(xhtml, kept) {
			t.Errorf("book lost %q:\n%s", kept, xhtml)
		}
	}
	if strings.Count(xhtml, "<h1") != 1 || strings.Contains(xhtml, "images/image-3") || files["OEBPS/images/image-1.png"] == "" || files["OEBPS/images/image-2.png"] == "" {
		t.Error("asset images were not shared or the title repeated")
	}
	if !strings.Contains(files["OEBPS/nav.xhtml"], `page.xhtml#price`) || !strings.Contains(files["OEBPS/content.opf"], `properties="svg"`) || !strings.Contains(files["OEBPS/content.opf"], "<dc:title>Bulbs &amp; lamps</dc:title>") {
		t.Errorf("book navigation or package is incomplete:\n%s\n%s", files["OEBPS/nav.xhtml"], files["OEBPS/content.opf"])
	}
}

func TestSendToKindleReportsMailFailure(t *testing.T) {
	smtp := newFakeSMTP(t, "right-password")
	app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, Mail: cairn.MailConfig{Server: smtp.addr, Password: "wrong-password", From: "owner@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app)
	defer srv.Close()
	if status, _, body := request(t, srv, "POST", "/api/pages", page("Bulb comparison"), token); status != 201 {
		t.Fatal(status, string(body))
	}
	if code, _, _ := authorForm(t, srv, "/settings", url.Values{"action": {"kindle"}, "kindle_address": {"me@kindle.com"}}, srv.URL); code != 303 {
		t.Fatal(code)
	}
	// A refused sign-in says so, and offers the guide rather than Try
	// again, which would fail the same way (p9).
	status, location := kindlePost(t, srv, "report", url.Values{"action": {"kindle"}})
	if status != 303 || location != "/?kindle=signin&page=report" {
		t.Fatal(status, location)
	}
	_, _, body := request(t, srv, "GET", location, nil, "")
	if !strings.Contains(string(body), "The mail server refused Cairn’s sign-in") || !strings.Contains(string(body), `<div class="toast is-error" data-toast>`) || strings.Contains(string(body), "Try again") || !strings.Contains(string(body), `troubleshooting.md#send-to-kindle">Mail troubleshooting</a>`) {
		t.Error("library hides the failure or its next step")
	}
	if _, _, _, data := smtp.message(); data != nil {
		t.Error("mail sent despite failed sign-in")
	}
}

func TestMailConfigIsValidated(t *testing.T) {
	for _, mail := range []cairn.MailConfig{
		{Server: "smtp.example.com", Password: "x", From: "owner@example.com"},
		{Server: "smtp.example.com:587", Password: "x", From: "Owner <owner@example.com>"},
		{Server: "smtp.example.com:587", From: "owner@example.com"},
	} {
		if app, err := cairn.Open(cairn.Config{DataDir: t.TempDir(), PublisherToken: token, Mail: mail}); err == nil {
			app.Close()
			t.Errorf("accepted %+v", mail)
		}
	}
}
