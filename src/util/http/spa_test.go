package http

import (
	"path/filepath"
	"testing"
)

func TestResolveSPAFilePath(t *testing.T) {
	root := filepath.Join("tmp", "www")
	indexPath := filepath.Join(root, "index.html")

	tests := []struct {
		name        string
		wildcard    string
		wantPath    string
		wantTryStat bool
	}{
		{
			name:        "relative asset path",
			wildcard:    "assets/app.js",
			wantPath:    filepath.Join(root, "assets", "app.js"),
			wantTryStat: true,
		},
		{
			name:        "absolute style asset path",
			wildcard:    "/assets/app.js",
			wantPath:    filepath.Join(root, "assets", "app.js"),
			wantTryStat: true,
		},
		{
			name:        "empty wildcard",
			wildcard:    "",
			wantPath:    indexPath,
			wantTryStat: false,
		},
		{
			name:        "dot wildcard",
			wildcard:    ".",
			wantPath:    indexPath,
			wantTryStat: false,
		},
		{
			name:        "directory traversal fallback",
			wildcard:    "../../etc/passwd",
			wantPath:    indexPath,
			wantTryStat: false,
		},
		{
			name:        "absolute directory traversal fallback",
			wildcard:    "/../../etc/passwd",
			wantPath:    filepath.Join(root, "etc", "passwd"),
			wantTryStat: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotTryStat := ResolveSPAFilePath(root, tt.wildcard)

			if gotTryStat != tt.wantTryStat {
				t.Fatalf("tryStat = %v, want %v", gotTryStat, tt.wantTryStat)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}
