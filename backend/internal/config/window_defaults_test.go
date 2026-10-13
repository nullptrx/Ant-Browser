package config

import "testing"

func TestDefaultWindowConfigForOS(t *testing.T) {
	if got := defaultWindowConfigForOS("darwin"); got.Width != 1440 || got.Height != 900 {
		t.Fatalf("darwin default = %+v, want 1440x900", got)
	}
	if got := defaultWindowConfigForOS("windows"); got.Width != 1750 || got.Height != 1000 {
		t.Fatalf("windows default = %+v, want 1750x1000", got)
	}
}

func TestIsLegacyDefaultWindowSizeOnlyMatchesDarwinOldDefault(t *testing.T) {
	cases := []struct {
		goos   string
		window WindowConfig
		want   bool
	}{
		{"darwin", WindowConfig{Width: 1750, Height: 1000}, true},
		{"darwin", WindowConfig{Width: 1600, Height: 1000}, false},
		{"windows", WindowConfig{Width: 1750, Height: 1000}, false},
		{"linux", WindowConfig{Width: 1750, Height: 1000}, false},
	}
	for _, tc := range cases {
		if got := isLegacyDefaultWindowSize(tc.goos, tc.window); got != tc.want {
			t.Fatalf("isLegacyDefaultWindowSize(%q, %+v) = %v, want %v", tc.goos, tc.window, got, tc.want)
		}
	}
}
