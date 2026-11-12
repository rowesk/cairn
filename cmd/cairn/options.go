package main

import (
	"errors"
	"flag"
	"io"
	"slices"
	"strings"
)

// flagSpec describes one flag. Every flag has a CAIRN_* environment variable derived from its name,
// unless it is local: publish's page flags describe one page and are read from the command line only.
type flagSpec struct {
	name, arg, def, help string
	local, boolean       bool
}

func (f flagSpec) env() string {
	return "CAIRN_" + strings.ToUpper(strings.ReplaceAll(f.name, "-", "_"))
}

type flagGroup struct {
	title string
	flags []flagSpec
}

// option is a flag's resolved value: the flag if given, else its environment variable, else the default.
// Empty values count as not given, so an empty line in an environment file changes nothing.
type option struct {
	spec    flagSpec
	value   string
	given   bool // set by the flag or its environment variable
	fromEnv bool
}

// label names the setting the way the person supplied it, so a message points at what to edit.
func (o *option) label() string {
	if o.fromEnv {
		return o.spec.env()
	}
	return "-" + o.spec.name
}

// either names both ways to supply a setting that was not given.
func (o *option) either() string {
	return "-" + o.spec.name + " or " + o.spec.env()
}

// shown is the setting and its value as the person wrote it.
func (o *option) shown() string {
	if o.fromEnv {
		return o.spec.env() + "=" + shellQuote(o.value)
	}
	return "-" + o.spec.name + " " + shellQuote(o.value)
}

type options map[string]*option

func (o options) get(name string) *option {
	opt, ok := o[name]
	if !ok {
		panic("cairn: undefined option " + name)
	}
	return opt
}

func (o options) value(name string) string { return o.get(name).value }

var errHelp = errors.New("help requested")

// parseOptions reads flags for one command, then fills anything not given from the environment.
// helpCommand is the help command that lists these flags, such as "help init".
func (c *cli) parseOptions(groups []flagGroup, args []string, helpCommand string) (options, error) {
	fs := flag.NewFlagSet("cairn", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	parsed := options{}
	var names []string
	for _, group := range groups {
		for _, spec := range group.flags {
			opt := &option{spec: spec}
			parsed[spec.name] = opt
			names = append(names, spec.name)
			fs.StringVar(&opt.value, spec.name, "", "")
		}
	}
	seeFlags := "Run " + c.prog + " " + helpCommand + " to see the flags."
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		message := err.Error()
		if name, ok := strings.CutPrefix(message, "flag provided but not defined: "); ok {
			hint := ""
			if guess := suggest(strings.TrimLeft(name, "-"), names, nil); guess != "" {
				hint = " Did you mean -" + guess + "?"
			}
			return nil, usagef("Unknown flag %s.%s\n%s", name, hint, seeFlags)
		}
		if name, ok := strings.CutPrefix(message, "flag needs an argument: "); ok {
			if opt, known := parsed[strings.TrimLeft(name, "-")]; known {
				return nil, usagef("%s needs a value, in the form -%s %s.\n%s", name, opt.spec.name, opt.spec.arg, seeFlags)
			}
		}
		return nil, usagef("Cairn could not read the flags: %s.\n%s", message, seeFlags)
	}
	if fs.NArg() > 0 {
		extra := fs.Arg(0)
		if slices.Contains(commands, extra) && len(args) > fs.NArg() {
			flags := args[:len(args)-fs.NArg()]
			quoted := make([]string, 0, len(args))
			for _, arg := range append(append([]string{extra}, fs.Args()[1:]...), flags...) {
				quoted = append(quoted, shellQuote(arg))
			}
			return nil, usagef("Unexpected argument %q. Put the command before the flags:\n  %s %s", extra, c.prog, strings.Join(quoted, " "))
		}
		return nil, usagef("Unexpected argument %q.\nEvery flag starts with a dash, and a value with spaces needs quotes.\n%s", extra, seeFlags)
	}
	for _, opt := range parsed {
		switch {
		case opt.value != "":
			opt.given = true
		case c.getenv(opt.spec.env()) != "":
			opt.value, opt.given, opt.fromEnv = c.getenv(opt.spec.env()), true, true
		default:
			opt.value = opt.spec.def
		}
	}
	return parsed, nil
}
