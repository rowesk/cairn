package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializePreservesInstallation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	credential := filepath.Join(dir, "publisher-token")
	if err := initialize(dir, credential, "Alex"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(credential)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(credential)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	if err := initialize(dir, credential, "Someone else"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(credential)
	if string(before) != string(after) {
		t.Fatal("credential changed")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "cairn.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var author string
	if err := db.QueryRow("SELECT author FROM preferences WHERE id=1").Scan(&author); err != nil {
		t.Fatal(err)
	}
	if author != "Alex" {
		t.Fatal("existing author changed", author)
	}
	if err := os.Remove(credential); err != nil {
		t.Fatal(err)
	}
	if err := initialize(dir, credential, "Alex"); err == nil {
		t.Fatal("regenerated missing credential for existing database")
	}
}

func TestInitializeRejectsUnsafeCredential(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "short"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			credential := filepath.Join(dir, "token")
			if kind == "symlink" {
				os.Symlink("/does-not-exist", credential)
			} else {
				contents := "abcdefghijklmnopqrstuvwxyz0123456789"
				mode := os.FileMode(0600)
				if kind == "public" {
					mode = 0644
				}
				if kind == "short" {
					contents = "short"
				}
				if err := os.WriteFile(credential, []byte(contents), mode); err != nil {
					t.Fatal(err)
				}
			}
			if err := initialize(dir, credential, "Alex"); err == nil {
				t.Fatal("unsafe credential accepted")
			}
		})
	}
}
