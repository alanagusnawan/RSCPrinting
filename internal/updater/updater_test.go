package updater

import "testing"

func TestIsSemver(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0.1.0", true},
		{"v1.2.3", true},
		{"1.2.3-beta.1", true},
		{"287edfb", false},
		{"", false},
		{"   ", false},
		{"dev", false},
	}
	for _, c := range cases {
		if got := isSemver(c.in); got != c.want {
			t.Errorf("isSemver(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
