package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rowesk/cairn/internal/cairn"
)

func initCommand(args []string) error {
	flags := flag.NewFlagSet("cairn init", flag.ContinueOnError)
	data := flags.String("data", "./data", "persistent data directory")
	owner := flags.String("owner", "Owner", "default author for this installation")
	tokenFile := flags.String("publisher-token-file", "", "credential file; defaults to <data>/publisher-token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments after init flags")
	}
	if *tokenFile == "" {
		*tokenFile = filepath.Join(*data, "publisher-token")
	}
	if err := initialize(*data, *tokenFile, *owner); err != nil {
		return err
	}
	fmt.Printf("Cairn is ready. Existing settings and credentials were preserved.\nStart with:\n  cairn -data %q -publisher-token-file %q\nOpen http://127.0.0.1:8080 and use Settings to create agent keys.\n", *data, *tokenFile)
	return nil
}

func initialize(data, tokenFile, owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 100 || !utf8.ValidString(owner) {
		return fmt.Errorf("owner must contain 1 to 100 UTF-8 bytes")
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(tokenFile)
	var token string
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("credential must be a regular file readable only by its owner: %s", tokenFile)
		}
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(raw))
	} else if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(data, "cairn.sqlite")); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("existing installation has no credential at %s; supply its original publisher-token-file", tokenFile)
		}
		if err := os.MkdirAll(filepath.Dir(tokenFile), 0700); err != nil {
			return err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		token = hex.EncodeToString(raw)
		f, err := os.OpenFile(tokenFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(token + "\n")
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else {
		return err
	}
	app, err := cairn.Open(cairn.Config{DataDir: data, PublisherToken: token, Owner: owner})
	if err != nil {
		return err
	}
	return app.Close()
}
