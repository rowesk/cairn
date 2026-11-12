package cairn

import (
	"strings"
	"testing"
)

// Initials avatars take a tint from the name: the same every time, the same
// whatever the case or outer spaces, and spread across every tint.
func TestAvatarTintIsStableAndSpread(t *testing.T) {
	seen := map[int]bool{}
	for _, name := range []string{"Chris", "Claude Code", "Codex", "Hermes", "Research agent", "Owner", "Ada", "Grace", "Linus", "Margaret", "Ken", "Barbara", "Dennis", "Frances"} {
		tint := avatarTint(name)
		if tint < 1 || tint > avatarTints {
			t.Fatalf("%s: tint %d out of range", name, tint)
		}
		if avatarTint(name) != tint || avatarTint(" "+strings.ToUpper(name)+" ") != tint {
			t.Fatalf("%s: tint not stable", name)
		}
		seen[tint] = true
	}
	if len(seen) < 4 {
		t.Fatalf("tints barely vary: %v", seen)
	}
	if avatarTint("Chris") == avatarTint("Claude Code") {
		t.Error("the two authors the critique named still share a tint")
	}
}

// A blank import title comes from the file, as cairn publish reads it.
func TestImportTitleFromTheFile(t *testing.T) {
	for _, tc := range []struct{ name, data, want string }{
		{"dentist.md", "# Dentist, 4 November\n\nTwo fillings.", "Dentist, 4 November"},
		{"notes.md", "Intro line\n\n#  Trailing hashes  ##\n", "Trailing hashes"},
		{"plain.md", "No heading here.\n## Only a section", "plain"},
		{"page.html", "<html><head><title>Boiler &amp; radiators</title></head><body><h1>Other</h1></body></html>", "Boiler & radiators"},
		{"bare.html", "<p>No title</p>", "bare"},
		{"photo.png", "\x89PNG", "photo"},
	} {
		if got := importTitle(tc.name, []byte(tc.data)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Excerpts set list items apart and keep a short opening item.
func TestExcerptsJoinListItemsWithASeparator(t *testing.T) {
	html := `<main><h1 class="title-echo">Packing list</h1><ul><li>Light rain jacket</li><li>Two pairs of trainers, the hills are steep</li><li>Adapter (Type F)</li></ul></main>`
	excerpt, _ := pageText(html, "Packing list")
	if excerpt != "Light rain jacket · Two pairs of trainers, the hills are steep" {
		t.Fatalf("excerpt %q", excerpt)
	}
	excerpt, _ = pageText(`<main><p>First paragraph is long enough to count as an opening.</p><p>Second one.</p></main>`, "Title")
	if strings.Contains(excerpt, "·") {
		t.Fatalf("paragraphs joined with a list separator: %q", excerpt)
	}
}

func TestKindleAddressesAndSizes(t *testing.T) {
	for address, want := range map[string]bool{"me@kindle.com": true, "Me@Free.Kindle.com": true, "chris@gmail.com": false, "kindle.com@example.com": false} {
		if kindleLike(address) != want {
			t.Errorf("%s: %v", address, !want)
		}
	}
	for n, want := range map[int]string{12: "12 bytes", 2048: "2 KB", 245760: "240 KB", 3 << 20: "3.0 MB"} {
		if got := byteSize(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
	if savedURL("/lamp") != "/lamp?saved=1" || savedURL("/lamp?in=q%3Dx") != "/lamp?in=q%3Dx&saved=1" {
		t.Error("saved address", savedURL("/lamp"), savedURL("/lamp?in=q%3Dx"))
	}
}
