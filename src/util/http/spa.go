package http

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveSPAFilePath normalizes a wildcard SPA path and maps it under wwwRoot.
// It strips any leading slash and blocks path traversal outside wwwRoot.
// The second return value indicates whether the caller should try os.Stat
// against the returned path (true) or directly fall back to index.html (false).
func ResolveSPAFilePath(wwwRoot, wildcard string) (string, bool) {
	indexPath := filepath.Join(wwwRoot, "index.html")

	cleaned := strings.TrimPrefix(filepath.Clean(wildcard), "/")
	if cleaned == "" || cleaned == "." {
		return indexPath, false
	}

	requestedPath := filepath.Join(wwwRoot, cleaned)
	base := filepath.Clean(wwwRoot)
	resolved := filepath.Clean(requestedPath)

	if resolved != base && !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
		return indexPath, false
	}

	return requestedPath, true
}
