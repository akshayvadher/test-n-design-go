package db

import "testing"

func TestFileURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"relative", "migrations", "file://migrations"},
		{"windows absolute drive letter", `D:\test\migrations`, "file://D:/test/migrations"},
		{"posix absolute", "/var/lib/migrations", "file:///var/lib/migrations"},
		{"windows absolute with forward slashes", `C:/repo/migrations`, "file://C:/repo/migrations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fileURL(tc.in)
			if got != tc.want {
				t.Errorf("fileURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
