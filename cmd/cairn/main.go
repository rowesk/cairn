// Command cairn runs a Cairn library and sets up its data directory.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal, restore the default handlers so a second Ctrl+C stops Cairn at once.
	context.AfterFunc(ctx, stop)
	c := &cli{prog: programName(os.Args), stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv}
	code := c.run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// cli carries everything a command touches outside its arguments, so tests can run commands in process.
type cli struct {
	prog           string // how the person invoked Cairn, used in suggested commands
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
}

// usageError is a mistake in how Cairn was invoked. It exits with status 2, like the flag package.
type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func usagef(format string, args ...any) error {
	return &usageError{fmt.Sprintf(format, args...)}
}

func programName(args []string) string {
	if len(args) == 0 || args[0] == "" {
		return "cairn"
	}
	return args[0]
}

var commands = []string{"serve", "init", "publish", "version", "help"}

// commonGuesses maps words people try to the command they meant.
var commonGuesses = map[string]string{"start": "serve", "run": "serve", "server": "serve", "setup": "init", "post": "publish", "upload": "publish", "send": "publish", "save": "publish"}

func (c *cli) run(ctx context.Context, args []string) int {
	err := c.dispatch(ctx, args)
	if err == nil {
		return 0
	}
	if errors.As(err, new(silentFailure)) {
		return 1
	}
	fmt.Fprintln(c.stderr, err)
	var usage *usageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

func (c *cli) dispatch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.serve(ctx, nil, false)
	}
	switch name := args[0]; name {
	case "serve":
		return c.serve(ctx, args[1:], true)
	case "init":
		return c.initCommand(args[1:])
	case "publish":
		return c.publish(ctx, args[1:])
	case "version", "-version", "--version":
		return c.version(name, args[1:])
	case "help":
		return c.help(args[1:])
	}
	if strings.HasPrefix(args[0], "-") {
		return c.serve(ctx, args, false)
	}
	return c.unknownCommand(args[0])
}

func (c *cli) unknownCommand(name string) error {
	message := fmt.Sprintf("Unknown command %q.", name)
	if guess := suggest(name, commands, commonGuesses); guess != "" {
		message += fmt.Sprintf(" Did you mean %q?", guess)
	}
	return usagef("%s\nRun %s help to see the commands.", message, c.prog)
}

func (c *cli) version(name string, args []string) error {
	if len(args) > 0 {
		if name == "version" && isHelpArg(args[0]) {
			return c.print(versionHelp)
		}
		return usagef("Unexpected argument %q. %s takes no arguments.\nRun %s version to print the version.", args[0], name, c.prog)
	}
	_, err := fmt.Fprintf(c.stdout, "cairn %s (%s/%s, %s)\n", versionString(), runtime.GOOS, runtime.GOARCH, runtime.Version())
	return err
}

func (c *cli) help(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) || args[0] == "help" {
		return c.print(mainHelp())
	}
	if len(args) > 1 {
		return usagef("Unexpected argument %q. help takes one command name.\nRun %s help %s.", args[1], c.prog, args[0])
	}
	switch args[0] {
	case "serve":
		return c.print(serveHelp())
	case "init":
		return c.print(initHelp())
	case "publish":
		return c.print(publishHelp())
	case "version":
		return c.print(versionHelp)
	}
	message := fmt.Sprintf("Unknown command %q.", args[0])
	if guess := suggest(args[0], commands, commonGuesses); guess != "" {
		message += fmt.Sprintf(" Did you mean %q?", guess)
	}
	return usagef("%s\nThe commands are %s.", message, commandsList())
}

func (c *cli) print(text string) error {
	_, err := io.WriteString(c.stdout, text)
	return err
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// versionString is the release version, or for a local build the commit it was built from.
func versionString() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return version
	}
	result := version + "-" + revision[:min(7, len(revision))]
	if modified {
		result += "-modified"
	}
	return result
}

// suggest returns the candidate closest to word, if it is close enough to be a likely typo.
func suggest(word string, candidates []string, guesses map[string]string) string {
	if guess, ok := guesses[word]; ok {
		return guess
	}
	best, bestDistance := "", 3
	for _, candidate := range candidates {
		if d := editDistance(word, candidate); d < bestDistance && d < len(candidate) {
			best, bestDistance = candidate, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = slices.Min([]int{previous[j] + 1, current[j-1] + 1, previous[j-1] + cost})
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

// shellQuote returns s in a form a POSIX shell reads back as one word.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
