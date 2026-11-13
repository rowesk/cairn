package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rowesk/cairn/internal/cairn"
)

type serveConfig struct {
	data, publicBase     string
	privateBase          string
	allowedHosts         []string
	listen, publicListen *option
	token                string
	mail                 cairn.MailConfig
}

func (c *cli) serve(ctx context.Context, args []string, explicit bool) error {
	helpCommand := "help"
	if explicit {
		helpCommand = "help serve"
	}
	opts, err := c.parseOptions(serveGroups, args, helpCommand)
	if errors.Is(err, errHelp) {
		if explicit {
			return c.print(serveHelp())
		}
		return c.print(mainHelp())
	}
	if err != nil {
		return err
	}
	cfg, err := c.loadServe(opts)
	if err != nil {
		return err
	}
	return c.runServer(ctx, cfg)
}

// loadServe checks every setting before anything touches the data directory or the network.
func (c *cli) loadServe(opts options) (serveConfig, error) {
	cfg := serveConfig{
		data:         opts.value("data"),
		publicBase:   opts.value("public-base-url"),
		listen:       opts.get("listen"),
		publicListen: opts.get("public-listen"),
	}
	var err error
	if cfg.token, err = c.loadToken(opts.get("publisher-token-file"), cfg.data); err != nil {
		return cfg, err
	}
	if err := checkListen(cfg.listen); err != nil {
		return cfg, err
	}
	if err := checkSharing(cfg.publicListen, opts.get("public-base-url"), cfg.listen); err != nil {
		return cfg, err
	}
	if private := opts.get("private-base-url"); private.given {
		if !isOrigin(private.value) {
			return cfg, fmt.Errorf("%s must be an origin only, such as https://cairn.example.ts.net,\nwith no path, query or user name. It is set to %q.", private.label(), private.value)
		}
		cfg.privateBase = private.value
	}
	if hosts := opts.get("allowed-hosts"); hosts.given {
		for _, name := range strings.Split(hosts.value, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			if !cairn.ValidHostName(name) {
				return cfg, fmt.Errorf("%s lists %q, which is not a host name.\nList names only, separated by commas, such as cairn.home.example,mac-mini.", hosts.label(), name)
			}
			cfg.allowedHosts = append(cfg.allowedHosts, name)
		}
	}
	if cfg.mail, err = c.loadMail(opts); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// bothSet refuses a value given twice through the environment, once directly and once as a file.
func bothSet(file *option, direct string, getenv func(string) string) error {
	if file.fromEnv && getenv(direct) != "" {
		return fmt.Errorf("%s and %s are both set, so Cairn cannot tell which to use.\nUnset one of them.", file.spec.env(), direct)
	}
	return nil
}

func (c *cli) loadToken(file *option, data string) (string, error) {
	if err := bothSet(file, "CAIRN_PUBLISHER_TOKEN", c.getenv); err != nil {
		return "", err
	}
	if file.given {
		token, err := readSecret(file.value, "installation token file", file.label())
		if errors.Is(err, fs.ErrNotExist) && !libraryExists(data) {
			return "", fmt.Errorf("%w\nTo set up a new library and token instead, run:\n  %s init -data %s", err, c.prog, shellQuote(data))
		}
		if err != nil {
			return "", err
		}
		return token, checkToken(token, "in "+file.value)
	}
	if token := c.getenv("CAIRN_PUBLISHER_TOKEN"); token != "" {
		return token, checkToken(token, "in CAIRN_PUBLISHER_TOKEN")
	}
	path := filepath.Join(data, "publisher-token")
	token, err := readSecret(path, "installation token file", file.label())
	if errors.Is(err, fs.ErrNotExist) {
		if libraryExists(data) {
			return "", fmt.Errorf("No installation token was found for the library in %s.\nPass the token it was set up with, using %s or CAIRN_PUBLISHER_TOKEN.", data, file.label())
		}
		return "", fmt.Errorf("Cairn needs an installation token, and %s has none yet.\nSet up the data directory first:\n  %s init -data %s\nThen run this command again.", data, c.prog, shellQuote(data))
	}
	if err != nil {
		return "", err
	}
	return token, checkToken(token, "in "+path)
}

func libraryExists(data string) bool {
	_, err := os.Stat(filepath.Join(data, "cairn.sqlite"))
	return err == nil
}

func checkToken(token, where string) error {
	if len(token) >= 32 {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("The installation token %s is empty.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32", where)
	}
	return fmt.Errorf("The installation token %s is too short: it has %d characters and needs at least 32.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32", where, len(token))
}

// secretError keeps the cause of a failed read so callers can tell a missing file from an unreadable one.
type secretError struct {
	message string
	cause   error
}

func (e *secretError) Error() string { return e.message }
func (e *secretError) Unwrap() error { return e.cause }

// readSecret reads a credential file and drops its trailing line break.
func readSecret(path, what, setting string) (string, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		return strings.TrimRight(string(raw), "\r\n"), nil
	case errors.Is(err, fs.ErrNotExist):
		return "", &secretError{fmt.Sprintf("The %s %s does not exist.\nCheck the path given to %s.", what, path, setting), err}
	case errors.Is(err, fs.ErrPermission):
		return "", &secretError{fmt.Sprintf("Cairn cannot read the %s %s: permission denied.\nRun Cairn as the user that owns the file. ls -l %s shows who that is.", what, path, shellQuote(path)), err}
	case errors.Is(err, syscall.EISDIR):
		return "", &secretError{fmt.Sprintf("The %s %s is a directory.\nSet %s to the file itself.", what, path, setting), err}
	}
	return "", &secretError{fmt.Sprintf("Cairn cannot read the %s %s: %v.", what, path, unwrapPath(err)), err}
}

// unwrapPath drops the "open /path:" prefix when the message already names the path.
func unwrapPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

var (
	tailnet4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnet6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	// interfaceAddrs lists this machine's addresses. Tests replace it.
	interfaceAddrs = net.InterfaceAddrs
)

// checkListen allows only loopback and this machine's own Tailscale addresses, because the library has no login.
func checkListen(o *option) error {
	host, port, err := net.SplitHostPort(o.value)
	if err != nil || port == "" {
		example := "127.0.0.1:8080"
		if _, err := strconv.ParseUint(o.value, 10, 16); err == nil {
			example = "127.0.0.1:" + o.value
		}
		return fmt.Errorf("%s must be an IP address and port. It is set to %q.\nUse an address such as %s.", o.label(), o.value, example)
	}
	if host == "" {
		return fmt.Errorf("%s has no IP address, so Cairn would listen on every network.\nUse 127.0.0.1:%s to keep the library on this machine.", o.shown(), port)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		if strings.EqualFold(host, "localhost") {
			return fmt.Errorf("%s needs an IP address, not the name localhost.\nUse 127.0.0.1:%s instead.", o.label(), port)
		}
		return fmt.Errorf("%s needs an IP address, not the name %q.\nUse 127.0.0.1:%s for this machine only, or this machine's Tailscale IP\n(tailscale ip -4 prints it) to reach Cairn from your tailnet.", o.label(), host, port)
	}
	if ip.IsLoopback() {
		return nil
	}
	if !tailnet4.Contains(ip) && !tailnet6.Contains(ip) {
		return fmt.Errorf("Cairn will not listen on %s.\nThe library has no login screen, so it listens only on loopback or Tailscale IPs.\nSet %s to 127.0.0.1:%s, then reach it from your tailnet with:\n  tailscale serve --bg http://127.0.0.1:%s", o.value, o.label(), port, port)
	}
	addresses, err := interfaceAddrs()
	if err != nil {
		return fmt.Errorf("Cairn could not list this machine's network addresses to check %s: %v.", o.label(), err)
	}
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Addr() == ip {
			return nil
		}
	}
	return fmt.Errorf("%s is a Tailscale IP, but this machine does not have it.\nRun tailscale ip to see this machine's addresses,\nor set %s to 127.0.0.1:%s and use tailscale serve.", ip, o.label(), port)
}

func checkSharing(listen, base, private *option) error {
	if listen.given != base.given {
		set, missing := listen, base
		if base.given {
			set, missing = base, listen
		}
		name := func(o *option) string {
			if set.fromEnv {
				return o.spec.env()
			}
			return "-" + o.spec.name
		}
		return fmt.Errorf("%s is set but %s is not. Sharing needs both:\n"+
			"  %s is the loopback address your HTTPS proxy forwards to.\n"+
			"  %s is the public origin, such as https://reports.example.com.\n"+
			"Set both to share pages by link, or neither to keep Cairn private.", name(set), name(missing), name(listen), name(base))
	}
	if !listen.given {
		return nil
	}
	host, port, err := net.SplitHostPort(listen.value)
	ip, ipErr := netip.ParseAddr(host)
	if err != nil || port == "" || ipErr != nil || !ip.IsLoopback() {
		return fmt.Errorf("%s must be a loopback address, such as 127.0.0.1:8081.\nIt is set to %q. Your HTTPS proxy or tunnel forwards visitors\nto that address, so Cairn never faces the network itself.", listen.label(), listen.value)
	}
	if port != "0" && listen.value == private.value {
		return fmt.Errorf("%s and %s are both %s.\nGive shared pages their own port, such as 127.0.0.1:8081.", listen.label(), private.label(), listen.value)
	}
	if !isOrigin(base.value) {
		return fmt.Errorf("%s must be an origin only, such as https://reports.example.com,\nwith no path, query or user name. It is set to %q.", base.label(), base.value)
	}
	return nil
}

// isOrigin mirrors cairn.Open's check of the public and private base URLs.
func isOrigin(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}

// loadMail mirrors cairn.MailConfig's rules so each message can name the setting to change.
func (c *cli) loadMail(opts options) (cairn.MailConfig, error) {
	server, from, username, passwordFile := opts.get("smtp-server"), opts.get("mail-from"), opts.get("smtp-username"), opts.get("smtp-password-file")
	passwordEnv := c.getenv("CAIRN_SMTP_PASSWORD")
	var given []string
	for _, o := range []*option{from, username, passwordFile} {
		if o.given {
			given = append(given, o.label())
		}
	}
	if passwordEnv != "" {
		given = append(given, "CAIRN_SMTP_PASSWORD")
	}
	if !server.given {
		if len(given) == 0 {
			return cairn.MailConfig{}, nil
		}
		verb := "is"
		if len(given) > 1 {
			verb = "are"
		}
		return cairn.MailConfig{}, fmt.Errorf("Send to Kindle is only partly set up: %s %s set, but the mail server is not.\nSet %s to your mail server, such as smtp.gmail.com:587,\nor clear the other mail settings to turn Send to Kindle off. See docs/kindle.md.", joinAnd(given), verb, server.either())
	}
	if host, port, err := net.SplitHostPort(server.value); err != nil || host == "" || port == "" {
		return cairn.MailConfig{}, fmt.Errorf("%s must be host:port, such as smtp.gmail.com:587.\nIt is set to %q.", server.label(), server.value)
	}
	if !from.given {
		return cairn.MailConfig{}, fmt.Errorf("Send to Kindle needs a sending address.\nSet %s to the address Amazon accepts pages from,\nsuch as you@example.com.", from.either())
	}
	if address, err := mail.ParseAddress(from.value); err != nil || address.Address != from.value || len(from.value) > 254 || strings.ContainsAny(from.value, "\r\n<>, ") {
		return cairn.MailConfig{}, fmt.Errorf("%s must be a plain email address, such as you@example.com, with no name\nor angle brackets. It is set to %q.", from.label(), from.value)
	}
	if err := bothSet(passwordFile, "CAIRN_SMTP_PASSWORD", c.getenv); err != nil {
		return cairn.MailConfig{}, err
	}
	password := passwordEnv
	if passwordFile.given {
		var err error
		if password, err = readSecret(passwordFile.value, "mail password file", passwordFile.label()); err != nil {
			return cairn.MailConfig{}, err
		}
	}
	if password == "" {
		return cairn.MailConfig{}, fmt.Errorf("Send to Kindle needs the mail password.\nPut it in a file only Cairn can read, then set %s\nto its path. docs/kindle.md shows how.", passwordFile.either())
	}
	return cairn.MailConfig{Server: server.value, Username: username.value, Password: password, From: from.value}, nil
}

func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func openError(data string, err error, command string) error {
	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		next := "Stop that process first. Two Cairn processes must never share a data directory."
		if command == "init" {
			next = "The library is already set up, so init has nothing to do while Cairn runs."
		}
		return fmt.Errorf("Another Cairn process is already using the data directory %s.\n%s", data, next)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("Cairn cannot write to the data directory %s: permission denied.\nRun Cairn as the user that owns it, or choose another directory with -data.", data)
	}
	return fmt.Errorf("Cairn could not open the library in %s: %v.\nCheck that the disk has space and that the directory belongs to the user running Cairn.\ndocs/operations.md explains how to restore a backup.", data, err)
}

func listenError(o *option, address string, err error) error {
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		return fmt.Errorf("Cairn cannot listen on %s because another program is using that port.\nStop that program, or choose another port with %s.", address, o.label())
	case errors.Is(err, syscall.EACCES):
		return fmt.Errorf("Cairn cannot listen on %s: permission denied.\nPorts below 1024 need administrator rights. Choose a higher port with %s, such as 8080.", address, o.label())
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return fmt.Errorf("Cairn cannot listen on %s because this machine does not have that address.\nUse 127.0.0.1, or an address this machine has, in %s.", address, o.label())
	}
	return fmt.Errorf("Cairn cannot listen on %s: %v.", address, err)
}

func (c *cli) runServer(ctx context.Context, cfg serveConfig) error {
	logger := slog.New(slog.NewTextHandler(c.stderr, nil))
	app, err := cairn.Open(cairn.Config{DataDir: cfg.data, PublisherToken: cfg.token, PublicBaseURL: cfg.publicBase, PrivateBaseURL: cfg.privateBase, AllowedHosts: cfg.allowedHosts, Version: versionString(), Logger: logger, Mail: cfg.mail})
	if err != nil {
		return openError(cfg.data, err, "serve")
	}
	defer app.Close()
	makeServer := func(handler http.Handler) *http.Server {
		// Transfers can take minutes over Tailscale. Bound headers and idle
		// connections without imposing a deadline on uploads or downloads.
		return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	}
	privateListener, err := net.Listen("tcp", cfg.listen.value)
	if err != nil {
		return listenError(cfg.listen, cfg.listen.value, err)
	}
	servers := []*http.Server{makeServer(app)}
	listeners := []net.Listener{privateListener}
	defer func() {
		for _, server := range servers {
			server.Close()
		}
		for _, listener := range listeners {
			listener.Close()
		}
	}()
	var publicAddress net.Addr
	if cfg.publicListen.given {
		publicListener, err := net.Listen("tcp", cfg.publicListen.value)
		if err != nil {
			return listenError(cfg.publicListen, cfg.publicListen.value, err)
		}
		servers = append(servers, makeServer(app.PublicHandler()))
		listeners = append(listeners, publicListener)
		publicAddress = publicListener.Addr()
	}
	c.banner(cfg, privateListener.Addr(), publicAddress)
	done := make(chan error, len(servers))
	for i, server := range servers {
		go func() { done <- server.Serve(listeners[i]) }()
	}
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("Cairn stopped because a listener failed: %v.", err)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, server := range servers {
			if err := server.Shutdown(shutdown); err != nil {
				return fmt.Errorf("Cairn did not stop cleanly: %v.", err)
			}
		}
		fmt.Fprintln(c.stderr, "Cairn stopped.")
		return nil
	}
}

// banner says what this run is using. Scripts match the "Cairn public listener:" and
// "Cairn private listener:" lines, so keep their wording, and keep the public line first.
func (c *cli) banner(cfg serveConfig, private, public net.Addr) {
	var b strings.Builder
	data, err := filepath.Abs(cfg.data)
	if err != nil {
		data = cfg.data
	}
	fmt.Fprintf(&b, "Cairn %s\nData directory: %s\n", versionString(), data)
	if public != nil {
		fmt.Fprintf(&b, "Sharing: on, public links start %s\n", strings.TrimSuffix(cfg.publicBase, "/"))
	} else {
		b.WriteString("Sharing: off, docs/sharing.md explains how to turn it on\n")
	}
	if cfg.mail.Server != "" {
		fmt.Fprintf(&b, "Send to Kindle: on, from %s through %s\n", cfg.mail.From, cfg.mail.Server)
	} else {
		b.WriteString("Send to Kindle: off, docs/kindle.md explains how to turn it on\n")
	}
	if public != nil {
		fmt.Fprintf(&b, "Cairn public listener: %s\n", public)
	}
	fmt.Fprintf(&b, "Cairn private listener: %s\n", private)
	where := "on this machine"
	if tcp, ok := private.(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
		where = "from a device on your tailnet"
	}
	fmt.Fprintf(&b, "Open http://%s %s to read your library.\n", private, where)
	io.WriteString(c.stderr, b.String())
}
