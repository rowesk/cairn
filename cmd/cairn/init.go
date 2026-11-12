package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rowesk/cairn/internal/cairn"
)

type initResult struct {
	createdLibrary, createdToken bool
}

func (c *cli) initCommand(args []string) error {
	opts, err := c.parseOptions(initGroups, args, "help init")
	if errors.Is(err, errHelp) {
		return c.print(initHelp())
	}
	if err != nil {
		return err
	}
	data := opts.value("data")
	defaultToken := filepath.Join(data, "publisher-token")
	tokenFile := opts.value("publisher-token-file")
	if tokenFile == "" {
		tokenFile = defaultToken
	}
	owner := opts.get("owner")
	result, err := initialize(data, tokenFile, owner.value)
	if err != nil {
		return err
	}

	var b strings.Builder
	if result.createdLibrary {
		fmt.Fprintf(&b, "Created a Cairn library in %s.\n", absolute(data))
		if !result.createdToken {
			b.WriteString("It uses the installation token that was already there.\n")
		}
		fmt.Fprintf(&b, "  Owner: %s\n  Installation token: %s (only you can read it)\n", strings.TrimSpace(owner.value), absolute(tokenFile))
	} else {
		fmt.Fprintf(&b, "Cairn is already set up in %s. Its settings and installation token were kept.\n", absolute(data))
		if owner.given {
			fmt.Fprintf(&b, "%s was ignored because the library already has an owner.\nChange the default author in Settings instead.\n", owner.shown())
		}
	}
	start := c.prog + " serve -data " + shellQuote(data)
	if filepath.Clean(tokenFile) != filepath.Clean(defaultToken) {
		start += " -publisher-token-file " + shellQuote(tokenFile)
	}
	fmt.Fprintf(&b, "\nStart Cairn with:\n  %s\n", start)
	if result.createdLibrary {
		b.WriteString("Then open the address it prints and add agent keys in Settings.\n")
	}
	return c.print(b.String())
}

func absolute(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// initialize creates the data directory, credential and database, keeping any that already exist.
func initialize(data, tokenFile, owner string) (initResult, error) {
	var result initResult
	owner = strings.TrimSpace(owner)
	switch {
	case owner == "":
		return result, errors.New("The owner name is empty.\nPass a name with -owner, such as -owner \"Alex\".")
	case !utf8.ValidString(owner):
		return result, errors.New("The owner name is not valid UTF-8 text.\nType the name again with -owner, such as -owner \"Alex\".")
	case len(owner) > 100:
		return result, fmt.Errorf("The owner name is too long: it is %d bytes and the limit is 100.\nUse a shorter name with -owner.", len(owner))
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return result, fmt.Errorf("Cairn cannot create the data directory %s: permission denied.\nChoose a folder you can write to with -data, or run init as the user that will run Cairn.", data)
		}
		return result, fmt.Errorf("Cairn cannot create the data directory %s: %v.", data, unwrapPath(err))
	}
	_, err := os.Stat(filepath.Join(data, "cairn.sqlite"))
	result.createdLibrary = errors.Is(err, fs.ErrNotExist)

	var token string
	info, err := os.Lstat(tokenFile)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("The installation token file %s is not a regular file.\nReplace it with the token file itself, not a link or folder, or choose another path with -publisher-token-file.", tokenFile)
		}
		if info.Mode().Perm()&0077 != 0 {
			return result, fmt.Errorf("Other users can read the installation token file %s.\nRestrict it to its owner, then run init again:\n  chmod 600 %s", tokenFile, shellQuote(tokenFile))
		}
		if token, err = readSecret(tokenFile, "installation token file", "-publisher-token-file"); err != nil {
			return result, err
		}
		token = strings.TrimSpace(token)
		if err := checkToken(token, "in "+tokenFile); err != nil {
			return result, err
		}
	case errors.Is(err, fs.ErrNotExist):
		if !result.createdLibrary {
			return result, fmt.Errorf("%s already holds a Cairn library, but its installation token is not at %s.\n"+
				"Pass the token file it was set up with, using -publisher-token-file.\n"+
				"init will not replace a library's token. If the token is lost, write a new one with:\n"+
				"  (umask 077; openssl rand -hex 32 > %s)", data, tokenFile, shellQuote(tokenFile))
		}
		if token, err = writeToken(tokenFile); err != nil {
			return result, err
		}
		result.createdToken = true
	default:
		return result, fmt.Errorf("Cairn cannot check the installation token file %s: %v.", tokenFile, unwrapPath(err))
	}
	app, err := cairn.Open(cairn.Config{DataDir: data, PublisherToken: token, Owner: owner})
	if err != nil {
		return result, openError(data, err, "init")
	}
	return result, app.Close()
}

func writeToken(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("Cairn cannot create the folder for the installation token %s: %v.", path, unwrapPath(err))
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("Cairn could not generate an installation token: %v.", err)
	}
	token := hex.EncodeToString(raw)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("Cairn cannot write the installation token file %s: %v.", path, unwrapPath(err))
	}
	_, writeErr := f.WriteString(token + "\n")
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return "", fmt.Errorf("Cairn cannot write the installation token file %s: %v.", path, writeErr)
	}
	return token, nil
}
