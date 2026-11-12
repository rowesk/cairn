package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// cairn publish sends one Markdown or HTML file to a library's API, so an
// agent never hand-builds JSON, escapes text or base64-encodes images.

var publishGroups = []flagGroup{
	{"Page", []flagSpec{
		{name: "title", arg: "TEXT", local: true,
			help: "Page title. Default the file's first # heading, or its <title>."},
		{name: "request", arg: "TEXT", local: true,
			help: "The owner's request in their own words. Required."},
		{name: "summary", arg: "TEXT", local: true,
			help: "The request from the owner's side, in at most 40 words."},
		{name: "source", arg: "NAME", local: true,
			help: "The app you publish from, such as Claude Code."},
		{name: "template", arg: "NAME", local: true,
			help: "report, comparison or visual. Markdown only. Default report."},
		{name: "asset", arg: "FILE", local: true,
			help: "An image to attach. Repeat for more. Images the file names as\n?asset=NAME are attached from beside it without this flag."},
		{name: "slug", arg: "NAME", local: true,
			help: "The page's address. Default a short random one."},
		{name: "replace", arg: "SLUG", local: true,
			help: "Replace this page instead of publishing a new one."},
		{name: "agent", arg: "NAME", local: true,
			help: "Byline, needed only with the installation token. An agent key\nsigns pages itself."},
	}},
	{"Output", []flagSpec{
		{name: "json", local: true, boolean: true,
			help: "Print Cairn's JSON reply instead of the address."},
	}},
	{"Connection", []flagSpec{
		{name: "url", arg: "URL",
			help: "The library's address, such as http://127.0.0.1:8080.\nCAIRN_API_KEY holds your agent key. No flag takes the key, so it\nnever shows in the process list."},
	}},
}

func publishHelp() string {
	return `Publish a Markdown or HTML file to your Cairn library as a private page,
and print its address.

Usage
  cairn publish FILE.md|FILE.html [flags]
  cairn publish - [flags]      Read Markdown from standard input.

` + renderGroups(publishGroups) + `
Examples
  Connect once, with an agent key from Settings, Authors:
    export CAIRN_URL=http://127.0.0.1:8080
    export CAIRN_API_KEY=cairn_...

  Publish a comparison with its chart image:
    cairn publish lamps.md -template comparison -asset chart.png \
      -request "find me a desk lamp under £100, warm light" \
      -summary "Find a warm desk lamp under £100."

  Replace that page later:
    cairn publish lamps.md -replace k3T -request "..."

Cairn answers with the page's address. A failed publish exits 1 and lists
every problem with the flag to change. Retries reuse one Idempotency-Key,
so a page is never saved twice.
`
}

// publishOptions is what a publish command asked for.
type publishOptions struct {
	file                                            string
	title, request, summary, source, template, slug string
	replace, agent, base                            string
	assets                                          []string
	json                                            bool
	baseFromEnv                                     bool
}

func (c *cli) parsePublish(args []string) (publishOptions, error) {
	var o publishOptions
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	textFlags := map[string]*string{"title": &o.title, "request": &o.request, "summary": &o.summary, "source": &o.source, "template": &o.template, "slug": &o.slug, "replace": &o.replace, "agent": &o.agent, "url": &o.base}
	var names []string
	for _, group := range publishGroups {
		for _, spec := range group.flags {
			names = append(names, spec.name)
			switch {
			case spec.name == "asset":
				fs.Func("asset", "", func(v string) error { o.assets = append(o.assets, v); return nil })
			case spec.boolean:
				fs.BoolVar(&o.json, spec.name, false, "")
			default:
				fs.StringVar(textFlags[spec.name], spec.name, "", "")
			}
		}
	}
	seeFlags := "Run " + c.prog + " help publish to see the flags."
	var files []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return o, errHelp
			}
			message := err.Error()
			if name, ok := strings.CutPrefix(message, "flag provided but not defined: "); ok {
				hint := ""
				if guess := suggest(strings.TrimLeft(name, "-"), names, map[string]string{"key": "url", "body": "request", "prompt": "request", "image": "asset", "images": "asset", "update": "replace", "layout": "template"}); guess != "" {
					hint = " Did you mean -" + guess + "?"
				}
				if strings.TrimLeft(name, "-") == "key" || strings.TrimLeft(name, "-") == "api-key" {
					return o, usagef("Unknown flag %s. Put the key in CAIRN_API_KEY instead, so it never shows\nin the process list.", name)
				}
				return o, usagef("Unknown flag %s.%s\n%s", name, hint, seeFlags)
			}
			if name, ok := strings.CutPrefix(message, "flag needs an argument: "); ok {
				for _, group := range publishGroups {
					for _, spec := range group.flags {
						if spec.name == strings.TrimLeft(name, "-") {
							return o, usagef("%s needs a value, in the form -%s %s.\n%s", name, spec.name, spec.arg, seeFlags)
						}
					}
				}
			}
			return o, usagef("Cairn could not read the flags: %s.\n%s", message, seeFlags)
		}
		if fs.NArg() == 0 {
			break
		}
		files = append(files, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	switch {
	case len(files) == 0:
		return o, usagef("Name the file to publish, such as:\n  %s publish notes.md -request \"...\"\n%s", c.prog, seeFlags)
	case len(files) > 1:
		return o, usagef("Unexpected argument %q. publish takes one file.\nA value with spaces needs quotes.\n%s", files[1], seeFlags)
	}
	o.file = files[0]
	if o.replace != "" && o.slug != "" && o.slug != o.replace {
		return o, usagef("-slug %s and -replace %s name different pages.\nA replaced page keeps its slug, so leave -slug out.", shellQuote(o.slug), shellQuote(o.replace))
	}
	if o.base == "" {
		o.base, o.baseFromEnv = c.getenv("CAIRN_URL"), true
	}
	return o, nil
}

// assetReference finds the images a page names as ?asset=NAME.
var assetReference = regexp.MustCompile(`\?asset=([A-Za-z0-9][A-Za-z0-9_-]{0,95}\.(?:png|jpe?g|gif|webp))`)

var markdownTitle = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*$`)
var htmlTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// publishRetryWait is how long publish waits before each retry. Tests shorten it.
var publishRetryWait = []time.Duration{time.Second, 3 * time.Second}

func (c *cli) publish(ctx context.Context, args []string) error {
	o, err := c.parsePublish(args)
	if errors.Is(err, errHelp) {
		return c.print(publishHelp())
	}
	if err != nil {
		return err
	}
	key := strings.TrimSpace(c.getenv("CAIRN_API_KEY"))
	setting := "CAIRN_URL"
	if !o.baseFromEnv {
		setting = "-url"
	}
	switch {
	case o.base == "":
		return fmt.Errorf("Cairn does not know where your library is.\nSet CAIRN_URL to its address, such as http://127.0.0.1:8080, or pass -url.")
	case key == "":
		return fmt.Errorf("CAIRN_API_KEY is not set.\nCreate an agent key in Cairn under Settings, Authors, then set\nCAIRN_API_KEY to it in this environment.")
	}
	base, err := url.Parse(strings.TrimRight(o.base, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return fmt.Errorf("%s must be the library's address, such as http://127.0.0.1:8080.\nIt is set to %q.", setting, o.base)
	}
	body, err := c.publication(o)
	if err != nil {
		return err
	}
	method, path := http.MethodPost, "/api/pages"
	if o.replace != "" {
		method, path = http.MethodPut, "/api/pages/"+url.PathEscape(o.replace)
	}
	var id [16]byte
	rand.Read(id[:])
	idempotency := "cairn-publish-" + hex.EncodeToString(id[:])
	status, reply, err := c.send(ctx, method, base.String()+path, key, idempotency, body)
	if err != nil {
		return err
	}
	if o.json {
		if _, err := c.stdout.Write(reply); err != nil {
			return err
		}
		if status >= 300 {
			return silentFailure{}
		}
		return nil
	}
	if status >= 300 {
		return c.publishFailure(o, base.String(), status, reply)
	}
	var page struct {
		URL        string  `json:"url"`
		PrivateURL *string `json:"private_url"`
		Warnings   []struct {
			Code, Message string
		} `json:"warnings"`
	}
	if err := json.Unmarshal(reply, &page); err != nil || page.URL == "" {
		return fmt.Errorf("%s answered %d with something that is not Cairn's API.\nCheck %s.", base, status, setting)
	}
	address := base.String() + page.URL
	if page.PrivateURL != nil {
		address = *page.PrivateURL
	}
	if _, err := fmt.Fprintln(c.stdout, address); err != nil {
		return err
	}
	for _, w := range page.Warnings {
		// The address above is already full, so this one says nothing new.
		if w.Code != "private_url_unset" {
			fmt.Fprintln(c.stderr, w.Message)
		}
	}
	return nil
}

// silentFailure exits 1 without a message, because -json already printed it.
type silentFailure struct{}

func (silentFailure) Error() string { return "" }

// publication reads the file and images into the API's JSON body.
func (c *cli) publication(o publishOptions) ([]byte, error) {
	var data []byte
	var err error
	format := ""
	switch strings.ToLower(filepath.Ext(o.file)) {
	case ".md", ".markdown":
		format = "markdown"
	case ".html", ".htm":
		format = "html"
	}
	if o.file == "-" {
		format = "markdown"
		stdin := c.stdin
		if stdin == nil {
			stdin = os.Stdin
		}
		data, err = io.ReadAll(io.LimitReader(stdin, 16<<20))
		if err != nil {
			return nil, fmt.Errorf("Cairn could not read standard input: %v.", err)
		}
	} else {
		if format == "" {
			return nil, usagef("Cairn publishes Markdown (.md) or HTML (.html) files, and %s is neither.\nRename it, or pipe Markdown in with: %s publish - < %s", o.file, c.prog, shellQuote(o.file))
		}
		if data, err = readFile(o.file, "file"); err != nil {
			return nil, err
		}
	}
	text := string(data)
	in := map[string]any{format: text}
	title := o.title
	if title == "" {
		if format == "markdown" {
			if m := markdownTitle.FindStringSubmatch(text); m != nil {
				title = m[1]
			}
		} else if m := htmlTitle.FindStringSubmatch(text); m != nil {
			title = strings.Join(strings.Fields(m[1]), " ")
		}
	}
	for name, value := range map[string]string{"title": title, "original_request": o.request, "request_summary": o.summary, "request_source": o.source, "template": o.template, "slug": o.slug, "agent": o.agent} {
		if value != "" {
			in[name] = value
		}
	}
	assets := map[string]string{}
	for _, path := range o.assets {
		image, err := readFile(path, "image")
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		if _, twice := assets[name]; twice {
			return nil, usagef("Two images are called %s. Rename one, because the page names images by file name.", name)
		}
		assets[name] = base64.StdEncoding.EncodeToString(image)
	}
	if o.file != "-" {
		dir := filepath.Dir(o.file)
		for _, m := range assetReference.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if _, given := assets[name]; given {
				continue
			}
			if image, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				assets[name] = base64.StdEncoding.EncodeToString(image)
			}
		}
	}
	if len(assets) > 0 {
		in["assets"] = assets
	}
	return json.Marshal(in)
}

func readFile(path, what string) ([]byte, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("The %s %s does not exist.", what, path)
	case errors.Is(err, fs.ErrPermission):
		return nil, fmt.Errorf("Cairn cannot read the %s %s: permission denied.", what, path)
	case errors.Is(err, syscall.EISDIR):
		return nil, fmt.Errorf("The %s %s is a directory. Name a file instead.", what, path)
	}
	return nil, fmt.Errorf("Cairn cannot read the %s %s: %v.", what, path, unwrapPath(err))
}

// send makes the request, retrying with the same Idempotency-Key when the
// network or the server says to try again.
func (c *cli) send(ctx context.Context, method, address, key, idempotency string, body []byte) (int, []byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotency)
		req.Header.Set("User-Agent", "cairn-publish/"+versionString())
		res, err := client.Do(req)
		var reply []byte
		if err == nil {
			reply, err = io.ReadAll(io.LimitReader(res.Body, 32<<20))
			res.Body.Close()
		}
		retry := false
		switch {
		case ctx.Err() != nil:
			return 0, nil, ctx.Err()
		case err != nil:
			var netErr net.Error
			retry = errors.As(err, &netErr) && netErr.Timeout() || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.ErrUnexpectedEOF)
		default:
			var failure struct{ Retryable bool }
			_ = json.Unmarshal(reply, &failure)
			retry = res.StatusCode >= 500 && failure.Retryable || res.StatusCode == http.StatusConflict && bytes.Contains(reply, []byte(`"idempotency_key_in_progress"`))
		}
		if !retry || attempt >= len(publishRetryWait) {
			if err != nil {
				return 0, nil, reachError(address, err)
			}
			return res.StatusCode, reply, nil
		}
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(publishRetryWait[attempt]):
		}
	}
}

func reachError(address string, err error) error {
	u, _ := url.Parse(address)
	origin := u.Scheme + "://" + u.Host
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("Cairn could not reach %s: nothing is listening there.\nCheck CAIRN_URL, and that Cairn is running.", origin)
	case strings.Contains(err.Error(), "no such host"):
		return fmt.Errorf("Cairn could not reach %s: that name does not resolve.\nCheck CAIRN_URL.", origin)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("Cairn did not answer at %s in time.\nCheck CAIRN_URL and your network. A retry will not save the page twice.", origin)
	}
	return fmt.Errorf("Cairn could not reach %s: %v.\nCheck CAIRN_URL.", origin, unwrapURL(err))
}

func unwrapURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// flagFor names the flag that sets an API field.
func flagFor(field string, o publishOptions) string {
	switch {
	case field == "title":
		return "-title"
	case field == "original_request":
		return "-request"
	case field == "request_summary":
		return "-summary"
	case field == "request_source":
		return "-source"
	case field == "template":
		return "-template"
	case field == "agent":
		return "-agent"
	case field == "slug" && o.replace != "":
		return "-replace"
	case field == "slug":
		return "-slug"
	case field == "markdown" || field == "html":
		return o.file
	case strings.HasPrefix(field, "assets."):
		return strings.TrimPrefix(field, "assets.")
	}
	return field
}

// publishFailure turns Cairn's error reply into lines naming each flag.
func (c *cli) publishFailure(o publishOptions, base string, status int, reply []byte) error {
	var failure struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
		Errors  []struct {
			Field, Message string
		} `json:"errors"`
	}
	if err := json.Unmarshal(reply, &failure); err != nil || failure.Code == "" {
		return fmt.Errorf("%s answered %d with something that is not Cairn's API.\nCheck CAIRN_URL.", base, status)
	}
	what := "publish " + o.file
	if o.replace != "" {
		what = "replace " + o.replace
	}
	if o.file == "-" && o.replace == "" {
		what = "publish the page"
	}
	switch failure.Code {
	case "authentication_required", "invalid_credential":
		return fmt.Errorf("Cairn did not accept the key in CAIRN_API_KEY. It may have been rotated or revoked.\nCreate a new key in Settings, Authors, then set CAIRN_API_KEY to it.")
	case "not_found":
		if o.replace != "" {
			return fmt.Errorf("No page has the slug %s.\nCheck -replace, or leave it out to publish a new page.", o.replace)
		}
		return fmt.Errorf("%s has no Cairn API.\nCheck CAIRN_URL: it should be the address you open the library at.", base)
	}
	var b strings.Builder
	if len(failure.Errors) > 1 || (len(failure.Errors) == 1 && failure.Errors[0].Field != "") {
		fmt.Fprintf(&b, "Cairn did not %s:\n", what)
		for _, e := range failure.Errors {
			if e.Field == "" {
				fmt.Fprintf(&b, "  %s\n", e.Message)
				continue
			}
			fmt.Fprintf(&b, "  %s: %s\n", flagFor(e.Field, o), e.Message)
		}
		b.WriteString("Fix these and run the command again.")
		return errors.New(b.String())
	}
	if failure.Field != "" {
		return fmt.Errorf("Cairn did not %s:\n  %s: %s", what, flagFor(failure.Field, o), failure.Message)
	}
	return fmt.Errorf("Cairn did not %s: %s", what, failure.Message)
}

// commandsList is how help names the commands.
func commandsList() string {
	names := slices.Clone(commands)
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
