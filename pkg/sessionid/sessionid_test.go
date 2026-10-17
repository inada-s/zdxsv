package sessionid

import "testing"

func TestDecode(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"0123505679", "12345678"},
		{"123505679", "12345678"}, // the lobby key1 may come unpadded
		{"0876604322", "87654321"},
		{"0000100001", "00000000"},
		{"9999999999", "99989998"},
	} {
		got, err := Decode(tc.value)
		if err != nil || got != tc.want {
			t.Errorf("Decode(%q) = %q, %v; want %q", tc.value, got, err, tc.want)
		}
	}
	for _, value := range []string{"", "01235056790", "01235x5679", "-123505679", "0000100000", "0000000000"} {
		if got, err := Decode(value); err == nil {
			t.Errorf("Decode(%q) = %q, want an error", value, got)
		}
	}
}
