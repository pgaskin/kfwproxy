package main

import "testing"

func TestExtractVersion(t *testing.T) {
	for _, c := range []struct {
		in  string
		out version
	}{
		{"", version{0, 0, 0}},
		{"1", version{0, 0, 0}},
		{"1.2", version{1, 2, 0}},
		{"1.2.3", version{1, 2, 3}},
		{"1.2.3.4", version{1, 2, 3}},
	} {
		for _, s := range []string{"", "asd", "."} {
			for _, e := range []string{"", "asd", ".", ".a123"} {
				v := extractVersion(s + c.in + e)
				if len(v) != 3 {
					t.Errorf("expected length of v to be 3, not %d", len(v))
				}
				if !v.Equal(c.out) {
					t.Errorf("expected %s, got %s", c.out, v)
				}
			}
		}
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b version
		c    bool
	}{
		{version{0, 0, 0}, version{0, 0, 0}, false},
		{version{0, 0, 1}, version{0, 0, 0}, false},
		{version{0, 0, 0}, version{0, 0, 1}, true},
		{version{0, 1, 0}, version{0, 0, 0}, false},
		{version{0, 0, 0}, version{0, 1, 0}, true},
		{version{1, 0, 0}, version{0, 0, 0}, false},
		{version{0, 0, 0}, version{1, 0, 0}, true},
		{version{1, 2, 3}, version{1, 2, 3}, false},
		{version{1, 2, 3}, version{3, 2, 1}, true},
		{version{3, 2, 1}, version{1, 2, 3}, false},
		{version{3, 2, 0}, version{1, 2, 3}, false},
		{version{3, 0, 0}, version{1, 2, 3}, false},
		{version{1, 1, 0}, version{1, 1, 0}, false},
		{version{1, 1, 0}, version{1, 0, 1}, false},
		{version{3, 19, 5761}, version{4, 15, 12920}, true},
		{version{4, 15, 12920}, version{3, 19, 5761}, false},
	} {
		if c.a.Less(c.b) != c.c {
			t.Errorf("%s < %s != %t", c.a, c.b, c.c)
		}
	}
}
