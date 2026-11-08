package strx

import "testing"

func TestMask(t *testing.T) {
	cases := []struct {
		s          string
		start, end int
		want       string
	}{
		{"13812345678", 3, 4, "138****5678"},
		{"110101199003071234", 6, 4, "110101********1234"},
		{"张三丰", 1, 0, "张**"},
		{"张三", 1, 1, "**"}, // nothing left to mask: mask everything
		{"abc", 2, 2, "***"},
		{"abc", 0, 0, "***"},
		{"abc", -1, -1, "***"},
		{"abcdef", 0, 2, "****ef"},
		{"", 1, 1, ""},
	}
	for _, c := range cases {
		if got := Mask(c.s, c.start, c.end); got != c.want {
			t.Errorf("Mask(%q, %d, %d) = %q, want %q", c.s, c.start, c.end, got, c.want)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct{ s, want string }{
		{"alice@example.com", "a****@example.com"},
		{"a@example.com", "*@example.com"},
		{"张三@example.com", "张*@example.com"},
		{"\"a@b\"@example.com", "\"****@example.com"},
		{"@example.com", "@example.com"},
		{"not-an-email", "n***********"},
		{"", ""},
	}
	for _, c := range cases {
		if got := MaskEmail(c.s); got != c.want {
			t.Errorf("MaskEmail(%q) = %q, want %q", c.s, got, c.want)
		}
	}
}
