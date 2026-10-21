package store

import "testing"

func TestParseSkillMD(t *testing.T) {
	cases := []struct{ md, name, desc, body string }{
		{"---\nname: a\ndescription: \"one line\"\n---\n\n# A\n", "a", "one line", "# A"},
		{"---\nname: b\ndescription:\n  wrapped\n  lines\nlicense: MIT\n---\nbody", "b", "wrapped lines", "body"},
		{"---\nname: c\ndescription: >\n  folded\n  text\n---\nx", "c", "folded text", "x"},
		{"no front", "", "", "no front"},
	}
	for _, c := range cases {
		n, d, b := ParseSkillMD(c.md)
		if n != c.name || d != c.desc || b != c.body {
			t.Errorf("%q: got %q %q %q", c.md, n, d, b)
		}
	}
}
