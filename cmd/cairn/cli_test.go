package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rowesk/cairn/internal/cairn"
)

var testToken = strings.Repeat("t", 40)

type result struct {
	code           int
	stdout, stderr string
}

func mapEnv(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func runCLI(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	c := &cli{prog: "cairn", stdout: &stdout, stderr: &stderr, getenv: mapEnv(env)}
	code := c.run(context.Background(), args)
	return result{code, stdout.String(), stderr.String()}
}

// onlyLoopback stands in for a machine with no Tailscale addresses, so results do not depend on the test host.
func onlyLoopback(t *testing.T) {
	saved := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = saved })
}

func TestHelpAndVersionCommands(t *testing.T) {
	versionLine := fmt.Sprintf("cairn %s (%s/%s, %s)\n", versionString(), runtime.GOOS, runtime.GOARCH, runtime.Version())
	cases := []struct {
		args   []string
		stdout string
	}{
		{[]string{"help"}, mainHelp()},
		{[]string{"-h"}, mainHelp()},
		{[]string{"-help"}, mainHelp()},
		{[]string{"--help"}, mainHelp()},
		{[]string{"-data", "./x", "-h"}, mainHelp()},
		{[]string{"help", "help"}, mainHelp()},
		{[]string{"help", "-h"}, mainHelp()},
		{[]string{"help", "serve"}, serveHelp()},
		{[]string{"serve", "-h"}, serveHelp()},
		{[]string{"serve", "--help"}, serveHelp()},
		{[]string{"help", "init"}, initHelp()},
		{[]string{"init", "-h"}, initHelp()},
		{[]string{"init", "--help"}, initHelp()},
		{[]string{"help", "publish"}, publishHelp()},
		{[]string{"publish", "-h"}, publishHelp()},
		{[]string{"publish", "--help"}, publishHelp()},
		{[]string{"help", "version"}, versionHelp},
		{[]string{"version", "-h"}, versionHelp},
		{[]string{"version"}, versionLine},
		{[]string{"-version"}, versionLine},
		{[]string{"--version"}, versionLine},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := runCLI(t, nil, tc.args...)
			if got.code != 0 || got.stdout != tc.stdout || got.stderr != "" {
				t.Fatalf("code %d\nstdout:\n%s\nstderr:\n%s", got.code, got.stdout, got.stderr)
			}
		})
	}
}

func TestHelpListsEveryFlagAndVariable(t *testing.T) {
	for _, group := range serveGroups {
		for _, spec := range group.flags {
			for _, text := range []string{mainHelp(), serveHelp()} {
				if !strings.Contains(text, "  -"+spec.name+" "+spec.arg+" ") || !strings.Contains(text, spec.env()+"\n") {
					t.Errorf("serve help is missing -%s or %s", spec.name, spec.env())
				}
			}
		}
	}
	for _, group := range initGroups {
		for _, spec := range group.flags {
			if !strings.Contains(initHelp(), "  -"+spec.name+" "+spec.arg+" ") || !strings.Contains(initHelp(), spec.env()+"\n") {
				t.Errorf("init help is missing -%s or %s", spec.name, spec.env())
			}
		}
	}
	for _, variable := range []string{"CAIRN_PUBLISHER_TOKEN ", "CAIRN_SMTP_PASSWORD "} {
		if !strings.Contains(mainHelp(), variable) {
			t.Errorf("help does not mention %s", variable)
		}
	}
	for _, text := range []string{mainHelp(), serveHelp(), initHelp(), publishHelp(), versionHelp} {
		for line := range strings.SplitSeq(text, "\n") {
			if len(line) > 80 {
				t.Errorf("help line is wider than 80 columns: %q", line)
			}
		}
	}
	for _, command := range []string{"cairn [serve] [flags]", "cairn init [flags]", "cairn publish FILE", "cairn version", "cairn help [command]"} {
		if !strings.Contains(mainHelp(), command) {
			t.Errorf("main help does not list %q", command)
		}
	}
}

func TestEnvironmentFileListsEveryVariable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "cairn.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range serveGroups {
		for _, spec := range group.flags {
			if !strings.Contains(string(raw), spec.env()+"=") {
				t.Errorf("deploy/cairn.env does not mention %s", spec.env())
			}
		}
	}
}

func TestBadInvocations(t *testing.T) {
	onlyLoopback(t)
	token := map[string]string{"CAIRN_PUBLISHER_TOKEN": testToken}
	with := func(extra map[string]string) map[string]string {
		env := map[string]string{"CAIRN_PUBLISHER_TOKEN": testToken}
		for k, v := range extra {
			env[k] = v
		}
		return env
	}
	sharing := []string{"-public-listen", "127.0.0.1:8081", "-public-base-url", "https://reports.example.com"}
	cases := []struct {
		name   string
		args   []string
		env    map[string]string
		code   int
		stderr string
	}{
		// Commands and flags.
		{"unknown command", []string{"bogus"}, nil, 2, "Unknown command \"bogus\".\nRun cairn help to see the commands.\n"},
		{"command typo", []string{"serv"}, nil, 2, "Unknown command \"serv\". Did you mean \"serve\"?\nRun cairn help to see the commands.\n"},
		{"common guess", []string{"start"}, nil, 2, "Unknown command \"start\". Did you mean \"serve\"?\nRun cairn help to see the commands.\n"},
		{"unknown flag", []string{"-nope"}, nil, 2, "Unknown flag -nope.\nRun cairn help to see the flags.\n"},
		{"flag typo", []string{"serve", "-listne", "x"}, nil, 2, "Unknown flag -listne. Did you mean -listen?\nRun cairn help serve to see the flags.\n"},
		{"serve flag given to init", []string{"init", "-listen", "127.0.0.1:8080"}, nil, 2, "Unknown flag -listen.\nRun cairn help init to see the flags.\n"},
		{"flag without value", []string{"-listen"}, nil, 2, "-listen needs a value, in the form -listen IP:PORT.\nRun cairn help to see the flags.\n"},
		{"init flag without value", []string{"init", "-owner"}, nil, 2, "-owner needs a value, in the form -owner NAME.\nRun cairn help init to see the flags.\n"},
		{"stray argument", []string{"-data", "x", "stuff"}, nil, 2, "Unexpected argument \"stuff\".\nEvery flag starts with a dash, and a value with spaces needs quotes.\nRun cairn help to see the flags.\n"},
		{"serve argument", []string{"serve", "stuff"}, nil, 2, "Unexpected argument \"stuff\".\nEvery flag starts with a dash, and a value with spaces needs quotes.\nRun cairn help serve to see the flags.\n"},
		{"unquoted owner", []string{"init", "-owner", "Alex", "Example"}, nil, 2, "Unexpected argument \"Example\".\nEvery flag starts with a dash, and a value with spaces needs quotes.\nRun cairn help init to see the flags.\n"},
		{"command after flags", []string{"-data", "my data", "init", "-owner", "Bob"}, nil, 2, "Unexpected argument \"init\". Put the command before the flags:\n  cairn init -owner Bob -data 'my data'\n"},
		{"help for unknown command", []string{"help", "bogus"}, nil, 2, "Unknown command \"bogus\".\nThe commands are serve, init, publish, version and help.\n"},
		{"help typo", []string{"help", "int"}, nil, 2, "Unknown command \"int\". Did you mean \"init\"?\nThe commands are serve, init, publish, version and help.\n"},
		{"help with two commands", []string{"help", "init", "serve"}, nil, 2, "Unexpected argument \"serve\". help takes one command name.\nRun cairn help init.\n"},
		{"version argument", []string{"version", "now"}, nil, 2, "Unexpected argument \"now\". version takes no arguments.\nRun cairn version to print the version.\n"},
		{"version flag argument", []string{"--version", "now"}, nil, 2, "Unexpected argument \"now\". --version takes no arguments.\nRun cairn version to print the version.\n"},

		// Private listener.
		{"localhost", []string{"-listen", "localhost:8080"}, token, 1, "-listen needs an IP address, not the name localhost.\nUse 127.0.0.1:8080 instead.\n"},
		{"host name", []string{"-listen", "myhost:9000"}, token, 1, "-listen needs an IP address, not the name \"myhost\".\nUse 127.0.0.1:9000 for this machine only, or this machine's Tailscale IP\n(tailscale ip -4 prints it) to reach Cairn from your tailnet.\n"},
		{"port only", []string{"-listen", "8080"}, token, 1, "-listen must be an IP address and port. It is set to \"8080\".\nUse an address such as 127.0.0.1:8080.\n"},
		{"not an address", []string{"-listen", "here"}, token, 1, "-listen must be an IP address and port. It is set to \"here\".\nUse an address such as 127.0.0.1:8080.\n"},
		{"no host", []string{"-listen", ":8080"}, token, 1, "-listen :8080 has no IP address, so Cairn would listen on every network.\nUse 127.0.0.1:8080 to keep the library on this machine.\n"},
		{"wildcard", []string{"-listen", "0.0.0.0:8080"}, token, 1, "Cairn will not listen on 0.0.0.0:8080.\nThe library has no login screen, so it listens only on loopback or Tailscale IPs.\nSet -listen to 127.0.0.1:8080, then reach it from your tailnet with:\n  tailscale serve --bg http://127.0.0.1:8080\n"},
		{"LAN address", []string{"-listen", "192.168.1.20:9000"}, token, 1, "Cairn will not listen on 192.168.1.20:9000.\nThe library has no login screen, so it listens only on loopback or Tailscale IPs.\nSet -listen to 127.0.0.1:9000, then reach it from your tailnet with:\n  tailscale serve --bg http://127.0.0.1:9000\n"},
		{"wildcard from environment", nil, with(map[string]string{"CAIRN_LISTEN": "0.0.0.0:8080"}), 1, "Cairn will not listen on 0.0.0.0:8080.\nThe library has no login screen, so it listens only on loopback or Tailscale IPs.\nSet CAIRN_LISTEN to 127.0.0.1:8080, then reach it from your tailnet with:\n  tailscale serve --bg http://127.0.0.1:8080\n"},
		{"Tailscale address elsewhere", []string{"-listen", "100.100.100.100:8080"}, token, 1, "100.100.100.100 is a Tailscale IP, but this machine does not have it.\nRun tailscale ip to see this machine's addresses,\nor set -listen to 127.0.0.1:8080 and use tailscale serve.\n"},

		// Sharing.
		{"public listener alone", []string{"-public-listen", "127.0.0.1:8081"}, token, 1, "-public-listen is set but -public-base-url is not. Sharing needs both:\n  -public-listen is the loopback address your HTTPS proxy forwards to.\n  -public-base-url is the public origin, such as https://reports.example.com.\nSet both to share pages by link, or neither to keep Cairn private.\n"},
		{"public base alone", []string{"-public-base-url", "https://reports.example.com"}, token, 1, "-public-base-url is set but -public-listen is not. Sharing needs both:\n  -public-listen is the loopback address your HTTPS proxy forwards to.\n  -public-base-url is the public origin, such as https://reports.example.com.\nSet both to share pages by link, or neither to keep Cairn private.\n"},
		{"public base alone from environment", nil, with(map[string]string{"CAIRN_PUBLIC_BASE_URL": "https://reports.example.com"}), 1, "CAIRN_PUBLIC_BASE_URL is set but CAIRN_PUBLIC_LISTEN is not. Sharing needs both:\n  CAIRN_PUBLIC_LISTEN is the loopback address your HTTPS proxy forwards to.\n  CAIRN_PUBLIC_BASE_URL is the public origin, such as https://reports.example.com.\nSet both to share pages by link, or neither to keep Cairn private.\n"},
		{"public listener on LAN", []string{"-public-listen", "192.168.1.20:8081", "-public-base-url", "https://reports.example.com"}, token, 1, "-public-listen must be a loopback address, such as 127.0.0.1:8081.\nIt is set to \"192.168.1.20:8081\". Your HTTPS proxy or tunnel forwards visitors\nto that address, so Cairn never faces the network itself.\n"},
		{"public listener on private port", []string{"-public-listen", "127.0.0.1:8080", "-public-base-url", "https://reports.example.com"}, token, 1, "-public-listen and -listen are both 127.0.0.1:8080.\nGive shared pages their own port, such as 127.0.0.1:8081.\n"},
		{"public base with path", []string{"-public-listen", "127.0.0.1:8081", "-public-base-url", "https://example.com/reports"}, token, 1, "-public-base-url must be an origin only, such as https://reports.example.com,\nwith no path, query or user name. It is set to \"https://example.com/reports\".\n"},
		{"public base without scheme", []string{"-public-listen", "127.0.0.1:8081", "-public-base-url", "reports.example.com"}, token, 1, "-public-base-url must be an origin only, such as https://reports.example.com,\nwith no path, query or user name. It is set to \"reports.example.com\".\n"},

		// Send to Kindle.
		{"mail address alone", append([]string{"-mail-from", "me@example.com"}, sharing...), token, 1, "Send to Kindle is only partly set up: -mail-from is set, but the mail server is not.\nSet -smtp-server or CAIRN_SMTP_SERVER to your mail server, such as smtp.gmail.com:587,\nor clear the other mail settings to turn Send to Kindle off. See docs/kindle.md.\n"},
		{"mail settings without server", []string{"-mail-from", "me@example.com"}, with(map[string]string{"CAIRN_SMTP_USERNAME": "me", "CAIRN_SMTP_PASSWORD": "secret"}), 1, "Send to Kindle is only partly set up: -mail-from, CAIRN_SMTP_USERNAME and CAIRN_SMTP_PASSWORD are set, but the mail server is not.\nSet -smtp-server or CAIRN_SMTP_SERVER to your mail server, such as smtp.gmail.com:587,\nor clear the other mail settings to turn Send to Kindle off. See docs/kindle.md.\n"},
		{"mail server without port", []string{"-smtp-server", "smtp.gmail.com"}, token, 1, "-smtp-server must be host:port, such as smtp.gmail.com:587.\nIt is set to \"smtp.gmail.com\".\n"},
		{"mail server without address", []string{"-smtp-server", "smtp.gmail.com:587"}, token, 1, "Send to Kindle needs a sending address.\nSet -mail-from or CAIRN_MAIL_FROM to the address Amazon accepts pages from,\nsuch as you@example.com.\n"},
		{"mail address with name", []string{"-smtp-server", "smtp.gmail.com:587", "-mail-from", "Me <me@example.com>"}, token, 1, "-mail-from must be a plain email address, such as you@example.com, with no name\nor angle brackets. It is set to \"Me <me@example.com>\".\n"},
		{"mail address from environment", nil, with(map[string]string{"CAIRN_SMTP_SERVER": "smtp.gmail.com:587", "CAIRN_MAIL_FROM": "not an address"}), 1, "CAIRN_MAIL_FROM must be a plain email address, such as you@example.com, with no name\nor angle brackets. It is set to \"not an address\".\n"},
		{"mail without password", []string{"-smtp-server", "smtp.gmail.com:587", "-mail-from", "me@example.com"}, token, 1, "Send to Kindle needs the mail password.\nPut it in a file only Cairn can read, then set -smtp-password-file or CAIRN_SMTP_PASSWORD_FILE\nto its path. docs/kindle.md shows how.\n"},
		{"mail password file missing", []string{"-smtp-server", "smtp.gmail.com:587", "-mail-from", "me@example.com", "-smtp-password-file", "/nonexistent/smtp-password"}, token, 1, "The mail password file /nonexistent/smtp-password does not exist.\nCheck the path given to -smtp-password-file.\n"},

		{"mail password twice in the environment", []string{"-smtp-server", "smtp.gmail.com:587", "-mail-from", "me@example.com"}, with(map[string]string{"CAIRN_SMTP_PASSWORD": "secret", "CAIRN_SMTP_PASSWORD_FILE": "/run/secret"}), 1, "CAIRN_SMTP_PASSWORD_FILE and CAIRN_SMTP_PASSWORD are both set, so Cairn cannot tell which to use.\nUnset one of them.\n"},

		// Installation token held in the environment.
		{"short token", nil, map[string]string{"CAIRN_PUBLISHER_TOKEN": "short"}, 1, "The installation token in CAIRN_PUBLISHER_TOKEN is too short: it has 5 characters and needs at least 32.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32\n"},
		{"token twice in the environment", nil, map[string]string{"CAIRN_PUBLISHER_TOKEN": testToken, "CAIRN_PUBLISHER_TOKEN_FILE": "/run/token"}, 1, "CAIRN_PUBLISHER_TOKEN_FILE and CAIRN_PUBLISHER_TOKEN are both set, so Cairn cannot tell which to use.\nUnset one of them.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, tc.env, tc.args...)
			if got.code != tc.code || got.stderr != tc.stderr || got.stdout != "" {
				t.Fatalf("code %d, want %d\nstderr:\n%s\nwant:\n%s\nstdout:\n%s", got.code, tc.code, got.stderr, tc.stderr, got.stdout)
			}
		})
	}
}

func TestTokenMessages(t *testing.T) {
	library := func(t *testing.T) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "cairn.sqlite"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	tokenFile := func(t *testing.T, contents string) string {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := []struct {
		name string
		run  func(t *testing.T) (result, string)
	}{
		{"no token and no library", func(t *testing.T) (result, string) {
			dir := t.TempDir()
			return runCLI(t, nil, "-data", dir), fmt.Sprintf("Cairn needs an installation token, and %s has none yet.\nSet up the data directory first:\n  cairn init -data %s\nThen run this command again.\n", dir, shellQuote(dir))
		}},
		{"no arguments at all", func(t *testing.T) (result, string) {
			return runCLI(t, nil), "Cairn needs an installation token, and ./data has none yet.\nSet up the data directory first:\n  cairn init -data ./data\nThen run this command again.\n"
		}},
		{"library without token", func(t *testing.T) (result, string) {
			dir := library(t)
			return runCLI(t, nil, "-data", dir), fmt.Sprintf("No installation token was found for the library in %s.\nPass the token it was set up with, using -publisher-token-file or CAIRN_PUBLISHER_TOKEN.\n", dir)
		}},
		{"missing token file for a new library", func(t *testing.T) (result, string) {
			dir := t.TempDir()
			missing := filepath.Join(dir, "missing")
			return runCLI(t, nil, "-data", dir, "-publisher-token-file", missing), fmt.Sprintf("The installation token file %s does not exist.\nCheck the path given to -publisher-token-file.\nTo set up a new library and token instead, run:\n  cairn init -data %s\n", missing, shellQuote(dir))
		}},
		{"missing token file from environment", func(t *testing.T) (result, string) {
			dir := library(t)
			missing := filepath.Join(dir, "missing")
			return runCLI(t, map[string]string{"CAIRN_PUBLISHER_TOKEN_FILE": missing}, "-data", dir), fmt.Sprintf("The installation token file %s does not exist.\nCheck the path given to CAIRN_PUBLISHER_TOKEN_FILE.\n", missing)
		}},
		{"token file is a directory", func(t *testing.T) (result, string) {
			dir := t.TempDir()
			return runCLI(t, nil, "-data", dir, "-publisher-token-file", dir), fmt.Sprintf("The installation token file %s is a directory.\nSet -publisher-token-file to the file itself.\n", dir)
		}},
		{"unreadable token file", func(t *testing.T) (result, string) {
			if os.Geteuid() == 0 {
				t.Skip("root reads any file")
			}
			path := tokenFile(t, testToken)
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			return runCLI(t, nil, "-data", t.TempDir(), "-publisher-token-file", path), fmt.Sprintf("Cairn cannot read the installation token file %s: permission denied.\nRun Cairn as the user that owns the file. ls -l %s shows who that is.\n", path, shellQuote(path))
		}},
		{"empty token file", func(t *testing.T) (result, string) {
			path := tokenFile(t, "\n")
			return runCLI(t, nil, "-data", t.TempDir(), "-publisher-token-file", path), fmt.Sprintf("The installation token in %s is empty.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32\n", path)
		}},
		{"short token file", func(t *testing.T) (result, string) {
			path := tokenFile(t, "abc\n")
			return runCLI(t, nil, "-data", t.TempDir(), "-publisher-token-file", path), fmt.Sprintf("The installation token in %s is too short: it has 3 characters and needs at least 32.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32\n", path)
		}},
		{"short default token file", func(t *testing.T) (result, string) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "publisher-token"), []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			return runCLI(t, nil, "-data", dir), fmt.Sprintf("The installation token in %s is too short: it has 3 characters and needs at least 32.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32\n", filepath.Join(dir, "publisher-token"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, want := tc.run(t)
			if got.code != 1 || got.stderr != want {
				t.Fatalf("code %d\nstderr:\n%s\nwant:\n%s", got.code, got.stderr, want)
			}
		})
	}
}

func TestFlagsWinOverEnvironment(t *testing.T) {
	for command, groups := range map[string][]flagGroup{"serve": serveGroups, "init": initGroups} {
		for _, group := range groups {
			for _, spec := range group.flags {
				t.Run(command+" -"+spec.name, func(t *testing.T) {
					parse := func(env map[string]string, args ...string) *option {
						t.Helper()
						c := &cli{prog: "cairn", getenv: mapEnv(env)}
						opts, err := c.parseOptions(groups, args, "help")
						if err != nil {
							t.Fatal(err)
						}
						return opts.get(spec.name)
					}
					env := map[string]string{spec.env(): "from-env"}
					cases := []struct {
						name           string
						opt            *option
						value          string
						given, fromEnv bool
						label          string
					}{
						{"flag beats variable", parse(env, "-"+spec.name, "from-flag"), "from-flag", true, false, "-" + spec.name},
						{"flag with equals", parse(env, "--"+spec.name+"=from-flag"), "from-flag", true, false, "-" + spec.name},
						{"variable alone", parse(env), "from-env", true, true, spec.env()},
						{"empty flag falls back to variable", parse(env, "-"+spec.name, ""), "from-env", true, true, spec.env()},
						{"neither", parse(nil), spec.def, false, false, "-" + spec.name},
						{"empty variable is ignored", parse(map[string]string{spec.env(): ""}), spec.def, false, false, "-" + spec.name},
					}
					for _, tc := range cases {
						if tc.opt.value != tc.value || tc.opt.given != tc.given || tc.opt.fromEnv != tc.fromEnv || tc.opt.label() != tc.label {
							t.Errorf("%s: got value %q given %v env %v label %s", tc.name, tc.opt.value, tc.opt.given, tc.opt.fromEnv, tc.opt.label())
						}
					}
				})
			}
		}
	}
}

func TestCredentialPrecedence(t *testing.T) {
	dir := t.TempDir()
	write := func(name, contents string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	flagToken := strings.Repeat("f", 32)
	envFileToken := strings.Repeat("e", 32)
	envToken := strings.Repeat("v", 32)
	defaultToken := strings.Repeat("d", 32)
	flagFile, envFile := write("flag-token", flagToken+"\r\n"), write("env-token", envFileToken+"\n")
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("data", "publisher-token"), defaultToken+"\n")
	tokenCases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"flag file wins", map[string]string{"CAIRN_PUBLISHER_TOKEN_FILE": envFile, "CAIRN_PUBLISHER_TOKEN": envToken}, []string{"-publisher-token-file", flagFile}, flagToken},
		{"variable file", map[string]string{"CAIRN_PUBLISHER_TOKEN_FILE": envFile}, nil, envFileToken},
		{"variable token beats default file", map[string]string{"CAIRN_PUBLISHER_TOKEN": envToken}, nil, envToken},
		{"default file", nil, nil, defaultToken},
	}
	for _, tc := range tokenCases {
		t.Run(tc.name, func(t *testing.T) {
			c := &cli{prog: "cairn", getenv: mapEnv(tc.env)}
			opts, err := c.parseOptions(serveGroups, append([]string{"-data", data}, tc.args...), "help")
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := c.loadServe(opts)
			if err != nil || cfg.token != tc.want {
				t.Fatalf("token %q, err %v; want %q", cfg.token, err, tc.want)
			}
		})
	}
	passwordFile := write("smtp-password", "from-file\n")
	mailCases := []struct {
		name string
		env  map[string]string
		args []string
		want cairn.MailConfig
	}{
		{"no mail settings", nil, nil, cairn.MailConfig{}},
		{"password file beats password variable", map[string]string{"CAIRN_SMTP_PASSWORD": "from-env", "CAIRN_SMTP_SERVER": "env.example.com:587"}, []string{"-smtp-server", "smtp.example.com:465", "-mail-from", "me@example.com", "-smtp-password-file", passwordFile},
			cairn.MailConfig{Server: "smtp.example.com:465", From: "me@example.com", Password: "from-file"}},
		{"password variable", map[string]string{"CAIRN_SMTP_PASSWORD": "from-env", "CAIRN_SMTP_SERVER": "env.example.com:587", "CAIRN_MAIL_FROM": "env@example.com", "CAIRN_SMTP_USERNAME": "login"}, nil,
			cairn.MailConfig{Server: "env.example.com:587", From: "env@example.com", Username: "login", Password: "from-env"}},
	}
	for _, tc := range mailCases {
		t.Run(tc.name, func(t *testing.T) {
			c := &cli{prog: "cairn", getenv: mapEnv(tc.env)}
			opts, err := c.parseOptions(serveGroups, tc.args, "help")
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.loadMail(opts)
			if err != nil || got != tc.want {
				t.Fatalf("mail %+v, err %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestTailscaleAddressMustBelongToThisMachine(t *testing.T) {
	saved := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = saved })
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("100.101.102.103"), Mask: net.CIDRMask(32, 32)}, &net.IPNet{IP: net.ParseIP("fd7a:115c:a1e0::1"), Mask: net.CIDRMask(128, 128)}}, nil
	}
	for address, ok := range map[string]bool{
		"127.0.0.1:8080":           true,
		"[::1]:8080":               true,
		"100.101.102.103:8080":     true,
		"[fd7a:115c:a1e0::1]:8080": true,
		"100.101.102.104:8080":     false,
		"[fd7a:115c:a1e0::2]:8080": false,
		"10.0.0.1:8080":            false,
		"[2001:db8::1]:8080":       false,
		"localhost:8080":           false,
		"tailnet-name.ts.net:8080": false,
	} {
		err := checkListen(&option{spec: flagSpec{name: "listen"}, value: address})
		if (err == nil) != ok {
			t.Errorf("%s: %v", address, err)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"./data":            "./data",
		"/var/lib/cairn":    "/var/lib/cairn",
		"":                  "''",
		"my data":           "'my data'",
		"it's":              `'it'\''s'`,
		"$HOME/data":        "'$HOME/data'",
		"C:/x,y=z@host+1%2": "C:/x,y=z@host+1%2",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestInitFirstRunAndRerun(t *testing.T) {
	type step struct {
		args   []string
		env    map[string]string
		stdout string
	}
	cases := []struct {
		name  string
		steps func(dir string) []step
	}{
		{"first run then rerun", func(dir string) []step {
			data := filepath.Join(dir, "data")
			return []step{
				{[]string{"init", "-data", data, "-owner", " Alex "}, nil, fmt.Sprintf("Created a Cairn library in %s.\n  Owner: Alex\n  Installation token: %s (only you can read it)\n\nStart Cairn with:\n  cairn serve -data %s\nThen open the address it prints and add agent keys in Settings.\n", data, filepath.Join(data, "publisher-token"), data)},
				{[]string{"init", "-data", data}, nil, fmt.Sprintf("Cairn is already set up in %s. Its settings and installation token were kept.\n\nStart Cairn with:\n  cairn serve -data %s\n", data, data)},
				{[]string{"init", "-data", data, "-owner", "Bob Example"}, nil, fmt.Sprintf("Cairn is already set up in %s. Its settings and installation token were kept.\n-owner 'Bob Example' was ignored because the library already has an owner.\nChange the default author in Settings instead.\n\nStart Cairn with:\n  cairn serve -data %s\n", data, data)},
				{[]string{"init"}, map[string]string{"CAIRN_DATA": data, "CAIRN_OWNER": "Bob"}, fmt.Sprintf("Cairn is already set up in %s. Its settings and installation token were kept.\nCAIRN_OWNER=Bob was ignored because the library already has an owner.\nChange the default author in Settings instead.\n\nStart Cairn with:\n  cairn serve -data %s\n", data, data)},
			}
		}},
		{"token kept elsewhere", func(dir string) []step {
			data, token := filepath.Join(dir, "data"), filepath.Join(dir, "secrets", "token")
			return []step{
				{[]string{"init", "-data", data, "-publisher-token-file", token}, nil, fmt.Sprintf("Created a Cairn library in %s.\n  Owner: Owner\n  Installation token: %s (only you can read it)\n\nStart Cairn with:\n  cairn serve -data %s -publisher-token-file %s\nThen open the address it prints and add agent keys in Settings.\n", data, token, data, token)},
				{[]string{"init", "-data", data}, map[string]string{"CAIRN_PUBLISHER_TOKEN_FILE": token}, fmt.Sprintf("Cairn is already set up in %s. Its settings and installation token were kept.\n\nStart Cairn with:\n  cairn serve -data %s -publisher-token-file %s\n", data, data, token)},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i, s := range tc.steps(t.TempDir()) {
				got := runCLI(t, s.env, s.args...)
				if got.code != 0 || got.stdout != s.stdout || got.stderr != "" {
					t.Fatalf("step %d: code %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", i, got.code, got.stdout, s.stdout, got.stderr)
				}
			}
		})
	}
}

func TestInitUsesExistingToken(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(data, "publisher-token")
	if err := os.WriteFile(token, []byte(testToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, nil, "init", "-data", data, "-owner", "Alex")
	want := fmt.Sprintf("Created a Cairn library in %s.\nIt uses the installation token that was already there.\n  Owner: Alex\n  Installation token: %s (only you can read it)\n\nStart Cairn with:\n  cairn serve -data %s\nThen open the address it prints and add agent keys in Settings.\n", data, token, data)
	if got.code != 0 || got.stdout != want {
		t.Fatalf("code %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", got.code, got.stdout, want, got.stderr)
	}
	if raw, _ := os.ReadFile(token); string(raw) != testToken+"\n" {
		t.Fatal("init replaced an existing token")
	}
}

func TestInitErrors(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, dir string) (result, string)
	}{
		{"empty owner", func(t *testing.T, dir string) (result, string) {
			return runCLI(t, nil, "init", "-data", dir, "-owner", "  "), "The owner name is empty.\nPass a name with -owner, such as -owner \"Alex\".\n"
		}},
		{"long owner", func(t *testing.T, dir string) (result, string) {
			return runCLI(t, nil, "init", "-data", dir, "-owner", strings.Repeat("x", 101)), "The owner name is too long: it is 101 bytes and the limit is 100.\nUse a shorter name with -owner.\n"
		}},
		{"library without token", func(t *testing.T, dir string) (result, string) {
			if err := os.WriteFile(filepath.Join(dir, "cairn.sqlite"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			token := filepath.Join(dir, "publisher-token")
			return runCLI(t, nil, "init", "-data", dir), fmt.Sprintf("%s already holds a Cairn library, but its installation token is not at %s.\nPass the token file it was set up with, using -publisher-token-file.\ninit will not replace a library's token. If the token is lost, write a new one with:\n  (umask 077; openssl rand -hex 32 > %s)\n", dir, token, shellQuote(token))
		}},
		{"token readable by others", func(t *testing.T, dir string) (result, string) {
			token := filepath.Join(dir, "publisher-token")
			if err := os.WriteFile(token, []byte(testToken), 0644); err != nil {
				t.Fatal(err)
			}
			return runCLI(t, nil, "init", "-data", dir), fmt.Sprintf("Other users can read the installation token file %s.\nRestrict it to its owner, then run init again:\n  chmod 600 %s\n", token, shellQuote(token))
		}},
		{"token is a link", func(t *testing.T, dir string) (result, string) {
			token := filepath.Join(dir, "publisher-token")
			if err := os.Symlink("/does-not-exist", token); err != nil {
				t.Fatal(err)
			}
			return runCLI(t, nil, "init", "-data", dir), fmt.Sprintf("The installation token file %s is not a regular file.\nReplace it with the token file itself, not a link or folder, or choose another path with -publisher-token-file.\n", token)
		}},
		{"short token", func(t *testing.T, dir string) (result, string) {
			token := filepath.Join(dir, "publisher-token")
			if err := os.WriteFile(token, []byte("short\n"), 0600); err != nil {
				t.Fatal(err)
			}
			return runCLI(t, nil, "init", "-data", dir), fmt.Sprintf("The installation token in %s is too short: it has 5 characters and needs at least 32.\nUse the token cairn init created, or write a new one with: openssl rand -hex 32\n", token)
		}},
		{"data directory cannot be created", func(t *testing.T, dir string) (result, string) {
			if os.Geteuid() == 0 {
				t.Skip("root writes anywhere")
			}
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(dir, 0700) })
			data := filepath.Join(dir, "data")
			return runCLI(t, nil, "init", "-data", data), fmt.Sprintf("Cairn cannot create the data directory %s: permission denied.\nChoose a folder you can write to with -data, or run init as the user that will run Cairn.\n", data)
		}},
		{"library in use", func(t *testing.T, dir string) (result, string) {
			if got := runCLI(t, nil, "init", "-data", dir); got.code != 0 {
				t.Fatal(got.stderr)
			}
			app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: testToken})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { app.Close() })
			return runCLI(t, nil, "init", "-data", dir), fmt.Sprintf("Another Cairn process is already using the data directory %s.\nThe library is already set up, so init has nothing to do while Cairn runs.\n", dir)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, want := tc.run(t, t.TempDir())
			if got.code != 1 || got.stderr != want || got.stdout != "" {
				t.Fatalf("code %d\nstderr:\n%s\nwant:\n%s\nstdout:\n%s", got.code, got.stderr, want, got.stdout)
			}
		})
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// startServe runs Cairn until the banner appears, and returns its output and a function that stops it.
func startServe(t *testing.T, env map[string]string, args ...string) (*lockedBuffer, func() int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuffer{}
	c := &cli{prog: "cairn", stdout: io.Discard, stderr: out, getenv: mapEnv(env)}
	done := make(chan int, 1)
	go func() { done <- c.run(ctx, args) }()
	stopped := false
	stop := func() int {
		if stopped {
			return 0
		}
		stopped = true
		cancel()
		return <-done
	}
	t.Cleanup(func() { stop() })
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "to read your library.\n") {
		select {
		case code := <-done:
			stopped = true
			t.Fatalf("Cairn exited with %d before starting:\n%s", code, out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("Cairn did not start:\n%s", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return out, stop
}

func get(t *testing.T, url string) int {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func TestServePrintsBannerAndStops(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"CAIRN_PUBLISHER_TOKEN": testToken, "CAIRN_SMTP_SERVER": "127.0.0.1:2525", "CAIRN_MAIL_FROM": "owner@example.com", "CAIRN_SMTP_PASSWORD": "secret"}
	out, stop := startServe(t, env, "serve", "-data", dir, "-listen", "127.0.0.1:0", "-public-listen", "127.0.0.1:0", "-public-base-url", "https://reports.example.com/")
	pattern := regexp.MustCompile(`^Cairn (.+)
Data directory: (.+)
Sharing: on, public links start https://reports\.example\.com
Send to Kindle: on, from owner@example\.com through 127\.0\.0\.1:2525
Cairn public listener: (127\.0\.0\.1:\d+)
Cairn private listener: (127\.0\.0\.1:\d+)
Open http://(127\.0\.0\.1:\d+) on this machine to read your library\.
$`)
	match := pattern.FindStringSubmatch(out.String())
	if match == nil || match[1] != versionString() || match[2] != dir || match[4] != match[5] {
		t.Fatalf("unexpected banner:\n%s", out)
	}
	if code := get(t, "http://"+match[4]+"/settings"); code != http.StatusOK {
		t.Fatalf("private settings returned %d", code)
	}
	if code := get(t, "http://"+match[3]+"/settings"); code != http.StatusNotFound {
		t.Fatalf("public settings returned %d", code)
	}
	if code := stop(); code != 0 || !strings.HasSuffix(out.String(), "your library.\nCairn stopped.\n") {
		t.Fatalf("stop returned %d:\n%s", code, out)
	}
}

func TestServeFromEnvironmentWithInitToken(t *testing.T) {
	onlyLoopback(t)
	data := filepath.Join(t.TempDir(), "data")
	if got := runCLI(t, nil, "init", "-data", data); got.code != 0 {
		t.Fatal(got.stderr)
	}
	// CAIRN_LISTEN would be refused, so starting proves the flag won; CAIRN_DATA supplies the directory and its token.
	env := map[string]string{"CAIRN_DATA": data, "CAIRN_LISTEN": "0.0.0.0:8080"}
	out, stop := startServe(t, env, "-listen", "127.0.0.1:0")
	want := fmt.Sprintf("Cairn %s\nData directory: %s\nSharing: off, docs/sharing.md explains how to turn it on\nSend to Kindle: off, docs/kindle.md explains how to turn it on\nCairn private listener: 127.0.0.1:", versionString(), data)
	if !strings.HasPrefix(out.String(), want) || strings.Contains(out.String(), "public listener") {
		t.Fatalf("unexpected banner:\n%s\nwant prefix:\n%s", out, want)
	}
	stop()

	// With no flag, the environment supplies the listener.
	env["CAIRN_LISTEN"] = "127.0.0.1:0"
	env["CAIRN_PUBLIC_LISTEN"] = "127.0.0.1:0"
	env["CAIRN_PUBLIC_BASE_URL"] = "http://reports.example.com"
	out, stop = startServe(t, env)
	if !strings.Contains(out.String(), "Sharing: on, public links start http://reports.example.com\n") || !strings.Contains(out.String(), "Cairn public listener: 127.0.0.1:") {
		t.Fatalf("environment did not configure sharing:\n%s", out)
	}
	stop()
}

func TestServeReportsBusyPortAndDirectory(t *testing.T) {
	env := map[string]string{"CAIRN_PUBLISHER_TOKEN": testToken}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	address := busy.Addr().String()
	got := runCLI(t, env, "-data", t.TempDir(), "-listen", address)
	want := fmt.Sprintf("Cairn cannot listen on %s because another program is using that port.\nStop that program, or choose another port with -listen.\n", address)
	if got.code != 1 || got.stderr != want {
		t.Fatalf("code %d\nstderr:\n%s\nwant:\n%s", got.code, got.stderr, want)
	}
	got = runCLI(t, env, "-data", t.TempDir(), "-listen", "127.0.0.1:0", "-public-listen", address, "-public-base-url", "https://reports.example.com")
	want = fmt.Sprintf("Cairn cannot listen on %s because another program is using that port.\nStop that program, or choose another port with -public-listen.\n", address)
	if got.code != 1 || got.stderr != want {
		t.Fatalf("code %d\nstderr:\n%s\nwant:\n%s", got.code, got.stderr, want)
	}

	dir := t.TempDir()
	app, err := cairn.Open(cairn.Config{DataDir: dir, PublisherToken: testToken})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	got = runCLI(t, env, "-data", dir, "-listen", "127.0.0.1:0")
	want = fmt.Sprintf("Another Cairn process is already using the data directory %s.\nStop that process first. Two Cairn processes must never share a data directory.\n", dir)
	if got.code != 1 || got.stderr != want {
		t.Fatalf("code %d\nstderr:\n%s\nwant:\n%s", got.code, got.stderr, want)
	}
}

func TestListenErrorsSayWhatToChange(t *testing.T) {
	listen := &option{spec: flagSpec{name: "listen"}, fromEnv: true}
	fail := func(errno syscall.Errno) error {
		return &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", errno)}
	}
	cases := []struct {
		err  error
		want string
	}{
		{fail(syscall.EADDRINUSE), "Cairn cannot listen on 127.0.0.1:80 because another program is using that port.\nStop that program, or choose another port with CAIRN_LISTEN."},
		{fail(syscall.EACCES), "Cairn cannot listen on 127.0.0.1:80: permission denied.\nPorts below 1024 need administrator rights. Choose a higher port with CAIRN_LISTEN, such as 8080."},
		{fail(syscall.EADDRNOTAVAIL), "Cairn cannot listen on 127.0.0.1:80 because this machine does not have that address.\nUse 127.0.0.1, or an address this machine has, in CAIRN_LISTEN."},
		{errors.New("listen tcp: something odd"), "Cairn cannot listen on 127.0.0.1:80: listen tcp: something odd."},
	}
	for _, tc := range cases {
		if got := listenError(listen, "127.0.0.1:80", tc.err).Error(); got != tc.want {
			t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
		}
	}
}

func TestProgramNameIsWhatThePersonTyped(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{{nil, "cairn"}, {[]string{""}, "cairn"}, {[]string{"./cairn"}, "./cairn"}, {[]string{"/usr/local/bin/cairn", "init"}, "/usr/local/bin/cairn"}} {
		if got := programName(tc.args); got != tc.want {
			t.Errorf("programName(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
