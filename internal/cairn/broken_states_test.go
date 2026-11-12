package cairn

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// The p9 "broken states": things that looked broken or said something false.
// Each test here would have failed before its fix.

// Item 1: the owner's own name is refused beside the field, before any key
// dialog opens. The page hands the script the name to check against.
func TestConnectFormKnowsTheOwnersName(t *testing.T) {
	a := fxStart(t, "")
	_, body := a.get(t, "/settings")
	if !strings.Contains(body, `id="connect-agent" novalidate hidden data-owner="Owner"`) {
		t.Fatalf("the connect form does not carry the default author: %s", regexp.MustCompile(`<form class="connect-agent"[^>]*>`).FindString(body))
	}
	// The server still refuses whatever reaches it.
	code, _, said := a.form(t, "/settings", url.Values{"action": {"key_create"}, "name": {" owner "}})
	if code != 400 || !strings.Contains(said, "your own name") {
		t.Errorf("own-name key: %d %s", code, said)
	}
}

// Item 2: a taken link name never shows as an address after a save that
// failed on it.
func TestTakenLinkNameShowsNoAddress(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("taken", "Taken", "Request", "Body."))
	code, _, body := a.form(t, "/new", url.Values{"title": {"Mine"}, "markdown": {"Words."}, "slug": {"taken"}, "access": {"private"}})
	if code != 409 {
		t.Fatalf("taken name: %d", code)
	}
	if line := regexp.MustCompile(`<p class="address-full" id="address-full" aria-live="polite">(.*?)</p>`).FindStringSubmatch(body); line == nil || line[1] != "" {
		t.Errorf("the address line still names the taken address: %v", line)
	}
	if !strings.Contains(body, `id="page-slug-error"`) {
		t.Error("the taken name lost its error")
	}
}

// Item 3: an address that can never be delivered is refused and not saved;
// a working address that isn't Amazon's is saved with one warning.
func TestKindleAddressesAreCheckedBeforeSaving(t *testing.T) {
	for address, valid := range map[string]bool{
		"chris@kindle.com": true, "chris@free.kindle.com": true, "chris@gmail.com": true, "a.b+c@mail.example.co.uk": true,
		"chris@kindle": false, "chris@kindle.": false, "chris@.kindle.com": false, "chris@kindle..com": false, "chris@kindle.c": false, "chris": false, "Chris <chris@kindle.com>": false,
	} {
		if got := kindleAddressValid(address); got != valid {
			t.Errorf("%q: valid %v, want %v", address, got, valid)
		}
	}
	a := secStart(t, "", func(c *Config) {
		c.Mail = MailConfig{Server: "127.0.0.1:9", From: "me@example.com", Password: "x"}
	})
	code, _, body := a.do(t, "POST", "/settings", strings.NewReader(url.Values{"csrf": {a.s.formToken().String()}, "action": {"kindle"}, "kindle_address": {"chris@kindle"}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.private.URL, "Accept": "application/json"})
	if code != 400 || !strings.Contains(body, `"kindle_address":"Enter one email address, such as name@kindle.com."`) || strings.Contains(body, "warning") {
		t.Errorf("an undeliverable address: %d %s", code, body)
	}
	if a.s.kindleAddress() != "" {
		t.Errorf("an undeliverable address was saved: %q", a.s.kindleAddress())
	}
	code, _, body = a.do(t, "POST", "/settings", strings.NewReader(url.Values{"csrf": {a.s.formToken().String()}, "action": {"kindle"}, "kindle_address": {"chris@gmail.com"}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.private.URL, "Accept": "application/json"})
	if code != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"message":"Kindle address saved."`) || !strings.Contains(body, "@free.kindle.com") || a.s.kindleAddress() != "chris@gmail.com" {
		t.Errorf("a non-Kindle address: %d %s", code, body)
	}
}

// relayStep is a scripted mail server: it answers each verb as told, or
// hangs up when told "hang up".
func relayStep(t *testing.T, replies map[string]string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				in := bufio.NewReader(conn)
				say := func(line string) { io.WriteString(conn, line+"\r\n") }
				say("220 relay")
				for {
					line, err := in.ReadString('\n')
					if err != nil {
						return
					}
					verb := strings.ToUpper(strings.Fields(line + " x")[0])
					if reply, ok := replies[verb]; ok {
						if reply == "hang up" {
							return
						}
						say(reply)
						continue
					}
					switch verb {
					case "EHLO":
						say("250-relay\r\n250 AUTH PLAIN")
					case "AUTH":
						say("235 ok")
					case "DATA":
						say("354 go")
						for {
							line, err := in.ReadString('\n')
							if err != nil || line == ".\r\n" {
								break
							}
						}
						say("250 queued")
					case "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(conn)
		}
	}()
	return l.Addr().String()
}

// Item 4: a failed send says which of a few plain causes it was, and offers
// Try again only where trying again can help.
func TestSendToKindleNamesTheCause(t *testing.T) {
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	nobody := closed.Addr().String()
	closed.Close()
	for _, tc := range []struct {
		name, server, result, says string
		retry                      bool
	}{
		{"nobody answers", nobody, "unreachable", "couldn’t reach the mail server", true},
		{"hangs up at sign-in", relayStep(t, map[string]string{"AUTH": "hang up"}), "unreachable", "couldn’t reach the mail server", true},
		{"wrong password", relayStep(t, map[string]string{"AUTH": "535 5.7.8 bad credentials"}), "signin", "refused Cairn’s sign-in", false},
		{"no such mailbox", relayStep(t, map[string]string{"RCPT": "550 5.1.1 no such mailbox"}), "refused", "refused the message", false},
		{"message refused", relayStep(t, map[string]string{"DATA": "554 5.7.1 rejected"}), "refused", "refused the message", false},
		{"busy", relayStep(t, map[string]string{"RCPT": "451 4.3.0 try later"}), "busy", "asked Cairn to try later", true},
		{"sent", relayStep(t, nil), "sent", "Sent to your Kindle", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := secStart(t, "", func(c *Config) {
				c.Mail = MailConfig{Server: tc.server, From: "me@example.com", Password: "x"}
			})
			a.publish(t, "POST", "/api/pages", fxPage("page", "Page", "Request", "Body."))
			if code, _, _ := a.form(t, "/settings", url.Values{"action": {"kindle"}, "kindle_address": {"me@kindle.com"}}); code != 303 {
				t.Fatal("kindle address", code)
			}
			code, h, _ := a.form(t, "/manage/page", url.Values{"action": {"kindle"}, "return": {"reader"}})
			if code != 303 || h.Get("Location") != "/page?kindle="+tc.result {
				t.Fatalf("send: %d %s", code, h.Get("Location"))
			}
			_, body := a.get(t, h.Get("Location"))
			toast := regexp.MustCompile(`(?s)<div class="toast is-[a-z]+" data-toast>.*?class="toast-close"`).FindString(body)
			if !strings.Contains(toast, tc.says) || strings.Contains(toast, "check the mail account in the server’s settings") {
				t.Errorf("toast: %s", toast)
			}
			if got := strings.Contains(toast, ">Try again</button>"); got != tc.retry {
				t.Errorf("Try again offered %v, want %v: %s", got, tc.retry, toast)
			}
		})
	}
	// The kinds come from the step and the reply, whatever wraps them.
	if got := mailResult(mailStep("signin", errors.New("plain"))); got != "failed" {
		t.Errorf("an unknown error: %s", got)
	}
	if !kindleRetries["failed"] || kindleRetries["signin"] || kindleRetries["refused"] || kindleRetries["too-large"] {
		t.Error("retries offered where they cannot help")
	}
}

// Item 7: a visitor who mistypes once and retypes at once gets in. Repeated
// wrong passwords still pause, and the page says how long.
func TestOneWrongPasswordCostsNothing(t *testing.T) {
	a := secStart(t, "", nil)
	a.publish(t, "POST", "/api/pages", fxPage("locked", "Locked", "Request", "Body."))
	a.form(t, "/manage/locked", url.Values{"action": {"share"}})
	a.form(t, "/manage/locked", url.Values{"action": {"password"}, "password": {"right password"}})
	unlock := func(password string) (int, string, string) {
		code, h, body := a.send(t, "POST", "public:/locked", "", strings.NewReader(url.Values{"password": {password}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": a.public.URL, "X-Forwarded-For": "198.51.100.20"})
		return code, h.Get("Retry-After"), body
	}
	if code, retry, body := unlock("wrong password"); code != 401 || retry != "" || strings.Contains(body, "data-wait") || !strings.Contains(body, "Check it and try again.") {
		t.Fatalf("first wrong password: %d %q", code, retry)
	}
	if code, _, _ := unlock("right password"); code != 303 {
		t.Fatalf("the right password straight after one wrong one: %d", code)
	}
	if b := backoff(1, unlockClientCap); b != 0 {
		t.Errorf("the first failure waits %v", b)
	}
	if b := backoff(2, unlockClientCap); b != time.Second {
		t.Errorf("the second failure waits %v", b)
	}
	if got := gateWait(150); got != "Wait 3 minutes, then try again. Cairn pauses after repeated wrong passwords." {
		t.Errorf("long wait: %s", got)
	}
}

// Item 8: the request heading reads with separators, and private rows do not
// announce the default.
func TestScreenReadersHearSeparatorsNotDefaults(t *testing.T) {
	a := fxStart(t, "")
	page := fxPage("asked", "Asked", "What should I do?", "Body.")
	page["request_source"] = "Claude Code"
	a.publish(t, "POST", "/api/pages", page)
	_, body := a.get(t, "/asked")
	if !strings.Contains(body, `<span class="sr-only">, </span>via Claude Code</span>`) || !strings.Contains(body, `<span class="sr-only">, </span>only you see this`) {
		t.Errorf("request label runs its parts together: %s", regexp.MustCompile(`<h2 class="request-label".*?</h2>`).FindString(body))
	}
	_, list := a.get(t, "/")
	if strings.Contains(list, `<span class="sr-only">Private</span>`) {
		t.Error("a private row announces Private")
	}
}

// Item 9: blocks are set apart in the index; the snippet comes from the
// field holding the most searched words, the rarest first, and starts close
// to its first match.
func TestSearchSnippetsExplainTheHit(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(`<div class="card"><b>1,046 kWh</b><div>gas, up 61% as heating came on</div></div><p>Week <span>one</span></p>`))
	if body := bodyText(doc); !strings.Contains(body, "1,046 kWh\ngas, up 61%") || strings.Contains(body, "kWhgas") {
		t.Errorf("blocks run together: %q", body)
	}
	el, _ := html.Parse(strings.NewReader(`<td>12<div>pages</div></td>`))
	if got := readable(el); got != "12 pages" {
		t.Errorf("readable: %q", got)
	}
	terms := searchTerms("IronWolf drives")
	snippet, score := bestSnippet("…with drives. Two "+markOpen+"drives"+markClose+" fit any box here, though the rest of this sentence goes on.\nAll prices include two 8 TB Seagate "+markOpen+"IronWolf"+markClose+" "+markOpen+"drives"+markClose+" at £178 each…", terms, []int{0, 1}, 16)
	if !strings.Contains(snippet, markOpen+"IronWolf"+markClose) || strings.Contains(snippet, "fit any box") {
		t.Errorf("the snippet skips the rarer word or runs blocks together: %q", snippet)
	}
	if _, single := bestSnippet("a "+markOpen+"drives"+markClose+" b", terms, []int{0, 1}, 16); single >= score {
		t.Errorf("one term scored %d against both terms' %d", single, score)
	}
	long := "Day two runs from Grasmere to Rydal along the old coffin route. Flat, 6 km, with a stop at the " + markOpen + "Wordsworth" + markClose + " Museum at Dove Cottage before the bus back."
	got, _ := bestSnippet("…"+long, searchTerms("wordsworth"), []int{0}, 16)
	if lead, _, _ := strings.Cut(strings.TrimPrefix(got, "…"), markOpen); !strings.HasPrefix(got, "…") || utf8.RuneCountInString(lead) > snippetLead || !strings.HasSuffix(lead, "at the ") {
		t.Errorf("the snippet starts far before its match: %q", got)
	}
	if got, _ := bestSnippet("Find my closest "+markOpen+"dentist"+markClose+".", searchTerms("dentist"), []int{0}, 14); got != "Find my closest "+markOpen+"dentist"+markClose+"." {
		t.Errorf("a short request lost its start: %q", got)
	}
	a := fxStart(t, "")
	nas := fxPage("nas", "Two-bay boxes", "Compare quiet two-bay boxes, about £600 with drives.", "# Two-bay boxes\n\nAll prices include two 8 TB Seagate IronWolf drives at £178 each.")
	a.publish(t, "POST", "/api/pages", nas)
	a.publish(t, "POST", "/api/pages", fxPage("other", "Other", "Spare drives for the old box.", "Nothing about it."))
	a.s.waitIndexed()
	_, list := a.get(t, "/?q="+url.QueryEscape("IronWolf drives"))
	if !strings.Contains(list, `<span class="where">In the page:</span>`) || !strings.Contains(list, "<mark>IronWolf</mark>") {
		t.Errorf("the hit is explained by the field without the rarer word: %s", regexp.MustCompile(`<p class="snippet">.*?</p>`).FindString(list))
	}
}

// Item 14: a number keeps its unit or multiplication sign on its line, in
// prose, headings and table cells, never in code; everything else is left
// as it was.
func TestNumbersKeepTheirUnits(t *testing.T) {
	const nb = "\u00a0"
	for in, want := range map[string]string{
		"a design load of 6.1 kW at −2 °C outside": "a design load of 6.1" + nb + "kW at −2" + nb + "°C outside",
		"Needed at 45 °C flow (W)":                 "Needed at 45" + nb + "°C flow (W)",
		"Drives (2 × 8 TB)":                        "Drives (2" + nb + "×" + nb + "8" + nb + "TB)",
		"2 x 8 TB drives":                          "2" + nb + "x" + nb + "8" + nb + "TB drives",
		"11,900 kWh of gas at 90 % efficiency":     "11,900" + nb + "kWh of gas at 90" + nb + "% efficiency",
		"from 100 mm to 300 mm, 2.4 m away":        "from 100" + nb + "mm to 300" + nb + "mm, 2.4" + nb + "m away",
		"6–7 kW, or 6 to 7 kW":                     "6–7" + nb + "kW, or 6 to 7" + nb + "kW",
		"19 dB at 1 m and 6.1 W idle":              "19" + nb + "dB at 1" + nb + "m and 6.1" + nb + "W idle",
		// Unchanged: currency, percentages without a space, years, plain words.
		"£7,500 grant and £1,900 more":           "£7,500 grant and £1,900 more",
		"up 61% as heating came on":              "up 61% as heating came on",
		"In 2026 I asked 3 firms for 5 quotes":   "In 2026 I asked 3 firms for 5 quotes",
		"the 1930s semi has 2 men and 4 windows": "the 1930s semi has 2 men and 4 windows",
		"6 months, 14 minutes and 3 hours":       "6 months, 14 minutes and 3 hours",
		"no numbers here":                        "no numbers here",
	} {
		if got := joinUnits(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
	out, _, err := renderMarkdown("# Heat\n\nA load of 6.1 kW.\n\n## At 45 °C\n\n| Drives (2 × 8 TB) | Power |\n| --- | ---: |\n| IronWolf | 6.1 W |\n\n`6.1 kW` and\n\n```\n6.1 kW\n```\n", "Heat", "report")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"A load of 6.1\u00a0kW.", "At 45\u00a0°C", "Drives (2\u00a0×\u00a08\u00a0TB)", "6.1\u00a0W", "<code>6.1 kW</code>", "<code>6.1 kW\n</code>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page lacks %q:\n%s", want, out)
		}
	}
}

// Item 16: the error page says why once, under a heading that already says
// what happened.
func TestEditRefusalDoesNotRepeatItsHeading(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", map[string]any{"slug": "custom", "title": "Custom", "agent": "Hermes", "original_request": "R", "html": "<!doctype html><title>Custom</title><p>Mine.</p>"})
	code, body := a.get(t, "/edit/custom")
	if code != 409 || strings.Count(body, "can’t edit this page") != 2 || !strings.Contains(body, "It’s custom HTML from Hermes. Ask Hermes to publish a new version.") {
		t.Errorf("edit refusal: %d %s", code, regexp.MustCompile(`(?s)<main.*</main>`).FindString(body))
	}
	if note := a.s.editNote(savedPage{Agent: "Hermes", FileName: "notes.pdf"}); note != "Cairn can’t edit this page because it came from a PDF file. Change the original and import it again." {
		t.Errorf("manage note: %s", note)
	}
}

// Item 17: Save title waits for an edit, Save sharing is the section's one
// primary button and Remove password sits under the password as a quiet
// link.
func TestManageSharingHasOnePrimaryAction(t *testing.T) {
	a := fxStart(t, "")
	a.publish(t, "POST", "/api/pages", fxPage("p", "Page", "Request", "Body."))
	a.form(t, "/manage/p", url.Values{"action": {"share"}})
	a.form(t, "/manage/p", url.Values{"action": {"password"}, "password": {"a long password"}})
	_, body := a.get(t, "/manage/p")
	if !strings.Contains(body, `<button class="quiet" name="action" value="rename" data-save-when-changed="page-title" data-off-until-changed>Save title</button>`) {
		t.Error("Save title does not wait for an edit")
	}
	if !strings.Contains(body, `<button name="action" value="sharing">Save sharing</button>`) {
		t.Error("Save sharing is not the primary button")
	}
	password := regexp.MustCompile(`(?s)<label class="field-label" for="share-password">.*?</div>\s*<div class="form-actions">`).FindString(body)
	if !strings.Contains(password, `<button class="quiet-link remove-password" name="action" value="remove_password" formnovalidate>Remove password</button>`) {
		t.Errorf("Remove password is not under the password field: %s", password)
	}
}

// The heading positions the reader's bar uses say how much space sits above
// each heading, and the reader accepts both message shapes.
func TestHeadingMessagesCarryTheSpaceAbove(t *testing.T) {
	if !strings.Contains(reportSizeJS, "gapAbove(h,top)") {
		t.Error("heading positions carry no space above")
	}
	reader := readerJS
	if !strings.Contains(reader, "item.length!==2&&item.length!==3") || !strings.Contains(reader, "const base=bar.offsetHeight||48") {
		t.Error("the reader does not name a section at the bar's bottom edge")
	}
}
