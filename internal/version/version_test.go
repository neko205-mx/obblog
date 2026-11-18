package version

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		tag  string
		want Parsed
		ok   bool
	}{
		{"v1.2.3", Parsed{1, 2, 3}, true},
		{"1.2.3", Parsed{1, 2, 3}, true},
		{"v1.2", Parsed{1, 2, 0}, true},
		{"v0.0.1", Parsed{0, 0, 1}, true},
		{"v1.2.3-rc1", Parsed{1, 2, 3}, true},
		{"v1.2.3+build5", Parsed{1, 2, 3}, true},
		{" v2.0.0 ", Parsed{2, 0, 0}, true},
		{"dev", Parsed{}, false},
		{"", Parsed{}, false},
		{"v", Parsed{}, false},
		{"v1", Parsed{}, false},
		{"v1.2.3.4", Parsed{}, false},
		{"v1.x.3", Parsed{}, false},
		{"v-1.2.3", Parsed{}, false},
	}
	for _, testCase := range cases {
		got, err := Parse(testCase.tag)
		if testCase.ok {
			if err != nil {
				t.Errorf("Parse(%q) unexpected error: %v", testCase.tag, err)
				continue
			}
			if got != testCase.want {
				t.Errorf("Parse(%q) = %+v, want %+v", testCase.tag, got, testCase.want)
			}
		} else if err == nil {
			t.Errorf("Parse(%q) should fail, got %+v", testCase.tag, got)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.3", "v1.2.4", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.3.0", "v1.2.9", 1},
		{"v1.2", "v1.2.0", 0},
		// A dev build sorts below every release so it always offers the update.
		{"v1.0.0", "dev", 1},
		{"dev", "v1.0.0", -1},
		{"dev", "dev", 0},
	}
	for _, testCase := range cases {
		if got := Compare(testCase.a, testCase.b); got != testCase.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", testCase.a, testCase.b, got, testCase.want)
		}
	}
}

func TestNewer(t *testing.T) {
	if !Newer("v1.3.0", "v1.2.0") {
		t.Error("v1.3.0 should be newer than v1.2.0")
	}
	// Equal versions must not count as an update, or the panel would offer a
	// no-op download forever.
	if Newer("v1.2.0", "v1.2.0") {
		t.Error("identical versions must not be an update")
	}
	if Newer("v1.1.0", "v1.2.0") {
		t.Error("older version must not be an update")
	}
	// Downgrade protection: a tampered tag must never look like an upgrade.
	if Newer("v1.2.0", "v1.3.0") {
		t.Error("downgrade must be rejected")
	}
}

func TestString(t *testing.T) {
	originalVersion, originalCommit := Version, Commit
	defer func() { Version, Commit = originalVersion, originalCommit }()

	Version, Commit = "v1.2.3", "abcdef1234567890"
	if got := String(); got != "v1.2.3 (abcdef1)" {
		t.Errorf("String() = %q", got)
	}

	Version, Commit = "v1.2.3", "unknown"
	if got := String(); got != "v1.2.3" {
		t.Errorf("String() with unknown commit = %q", got)
	}
}

func TestIsDev(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = "dev"
	if !IsDev() {
		t.Error("dev build should report IsDev")
	}
	Version = "v1.0.0"
	if IsDev() {
		t.Error("tagged build should not report IsDev")
	}
}
