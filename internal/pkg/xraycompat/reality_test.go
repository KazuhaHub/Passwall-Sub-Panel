package xraycompat

import "testing"

func TestMihomoNeedsMLKEM(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version string
		want    bool
	}{
		{version: "26.6.27", want: false},
		{version: "v26.7.28", want: false},
		{version: "26.9.7", want: false},
		{version: "26.9.8", want: true},
		{version: "v26.9.9", want: true},
		{version: "Xray 26.9.9", want: true},
		{version: "26.10.0", want: true},
		{version: "", want: false},
		{version: "latest", want: false},
		{version: "26.09.8", want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			if got := MihomoNeedsMLKEM(test.version); got != test.want {
				t.Fatalf("MihomoNeedsMLKEM(%q) = %v, want %v", test.version, got, test.want)
			}
		})
	}
}
