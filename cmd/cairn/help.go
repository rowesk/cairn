package main

import (
	"strings"
)

const helpWidth = 76

var serveGroups = []flagGroup{
	{"Listening", []flagSpec{
		{name: "listen", arg: "IP:PORT", def: "127.0.0.1:8080",
			help: "Address of the private library: 127.0.0.1, or this machine's\nTailscale IP. Default 127.0.0.1:8080."},
		{name: "data", arg: "DIR", def: "./data",
			help: "Folder for the database, pages and keys. Default ./data."},
		{name: "private-base-url", arg: "URL",
			help: "Address you and your agents use to reach the private library, such as\nhttps://cairn.example.ts.net. The API puts it in the links it returns."},
		{name: "allowed-hosts", arg: "NAMES",
			help: "Other names you open the library at, separated by commas, such as\ncairn.home.example. IP addresses, localhost, tailnet names and the\nprivate base URL always work; Cairn refuses every other name."},
	}},
	{"Sharing (set both to share pages by public link)", []flagSpec{
		{name: "public-listen", arg: "IP:PORT",
			help: "Loopback address that serves shared pages and nothing else, such as\n127.0.0.1:8081. Point your HTTPS proxy or tunnel at it."},
		{name: "public-base-url", arg: "URL",
			help: "Public origin used in share links, such as https://reports.example.com."},
	}},
	{"Credentials", []flagSpec{
		{name: "publisher-token-file", arg: "FILE",
			help: "File holding the installation token. Default DIR/publisher-token.\nCAIRN_PUBLISHER_TOKEN can hold the token itself instead."},
	}},
	{"Send to Kindle (set the mail account to turn it on)", []flagSpec{
		{name: "smtp-server", arg: "HOST:PORT",
			help: "Mail server that sends pages, such as smtp.gmail.com:587."},
		{name: "mail-from", arg: "ADDRESS",
			help: "Address the pages come from. Add it to Amazon's approved list."},
		{name: "smtp-username", arg: "NAME",
			help: "Mail server sign-in name. Default the -mail-from address."},
		{name: "smtp-password-file", arg: "FILE",
			help: "File holding the mail password. CAIRN_SMTP_PASSWORD can hold the\npassword itself instead."},
	}},
}

var initGroups = []flagGroup{
	{"Flags", []flagSpec{
		{name: "data", arg: "DIR", def: "./data",
			help: "Folder for the database, pages and keys. Default ./data."},
		{name: "owner", arg: "NAME", def: "Owner",
			help: "Default author for pages you write in Cairn. Used only when the\nlibrary is created; change it later in Settings. Default Owner."},
		{name: "publisher-token-file", arg: "FILE",
			help: "Where to keep the installation token. Default DIR/publisher-token."},
	}},
}

const envNote = `Each flag can also be set with the environment variable on its right.
A flag wins over its variable. Empty values are ignored.
`

const serveExamples = `Examples
  Set up a library in ./data, then start it:
    cairn init -data ./data -owner "Alex"
    cairn serve -data ./data

  Listen on this machine's Tailscale IP and share pages through a proxy:
    cairn serve -data /var/lib/cairn -listen 100.101.102.103:8080 \
      -public-listen 127.0.0.1:8081 \
      -public-base-url https://reports.example.com
`

const docsLink = "Guides: https://github.com/rowesk/cairn/tree/main/docs\n"

func mainHelp() string {
	return `Cairn keeps the pages your AI agents write in a private library you read
in a browser, and shares single pages by link when you choose.

Usage
  cairn [serve] [flags]   Run the library. Plain cairn does the same.
  cairn init [flags]      Set up a data directory and installation token.
  cairn publish FILE      Publish a Markdown or HTML file as a private page.
  cairn version           Print the version.
  cairn help [command]    Show help for serve, init, publish or version.

Flags for serve

` + renderGroups(serveGroups) + "\n" + envNote + "\n" + serveExamples + "\n" + docsLink
}

func serveHelp() string {
	return `Run the library. This is the default command, so plain cairn does the same.

Usage
  cairn serve [flags]

` + renderGroups(serveGroups) + "\n" + envNote + "\n" + serveExamples + "\n" + docsLink
}

func initHelp() string {
	return `Set up a Cairn library: the data directory, its database and a publisher
token that only you can read. Running init again keeps what is there.

Usage
  cairn init [flags]

` + renderGroups(initGroups) + "\n" + envNote + `
Example
  cairn init -data ./data -owner "Alex"
`
}

const versionHelp = `Print the Cairn version, the platform it was built for and its Go version.

Usage
  cairn version
`

// renderGroups lists flags under their group titles, each flag's environment variable at the right margin.
func renderGroups(groups []flagGroup) string {
	var b strings.Builder
	for i, group := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(group.title + "\n")
		for _, spec := range group.flags {
			left := "  -" + spec.name + " " + spec.arg
			if spec.local {
				b.WriteString(strings.TrimRight(left, " ") + "\n")
			} else {
				b.WriteString(left + strings.Repeat(" ", max(2, helpWidth-len(left)-len(spec.env()))) + spec.env() + "\n")
			}
			for line := range strings.SplitSeq(spec.help, "\n") {
				b.WriteString("      " + line + "\n")
			}
		}
	}
	return b.String()
}
