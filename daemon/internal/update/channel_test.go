package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadChannel(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	enabled := write("on.sources", "# comment\nTypes: deb\nURIs: https://example.invalid/latest/download\nSuites: ./\nSigned-By: /k.pgp\nEnabled: yes\n")
	disabled := write("off.sources", "Types: deb\nURIs: https://example.invalid/x\nSuites: ./\nEnabled: no\n")
	noKey := write("default.sources", "Types: deb\nURIs: https://example.invalid/y\nSuites: ./\n")
	twoStanzas := write("two.sources", "Types: deb\nURIs: https://example.invalid/a\nSuites: ./\n\nTypes: deb\nURIs: https://example.invalid/b\nSuites: ./\nEnabled: no\n")

	for _, c := range []struct {
		name string
		path string
		want Channel
	}{
		{"enabled", enabled, Channel{Enabled: true, URL: "https://example.invalid/latest/download"}},
		{"disabled", disabled, Channel{Enabled: false, URL: "https://example.invalid/x", Reason: ChannelDisabled}},
		{"enabled by default", noKey, Channel{Enabled: true, URL: "https://example.invalid/y"}},
		{"only the first stanza counts", twoStanzas, Channel{Enabled: true, URL: "https://example.invalid/a"}},
		{"missing file", filepath.Join(dir, "none.sources"), Channel{Reason: ChannelMissing}},
	} {
		if got := ReadChannel(c.path); got != c.want {
			t.Errorf("%s: ReadChannel = %+v, want %+v", c.name, got, c.want)
		}
	}
}
