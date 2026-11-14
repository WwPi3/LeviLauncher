package vcruntime

import (
	"bytes"
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/liteldev/LeviLauncher/internal/fsx"
)

// The 0.3.13 launchercore DLLs load this runtime from their private bin folder.
// Embed it here so the dependency can be reconstructed after the folder is
// removed, without relying on a temporary or separately downloaded copy.
//
//go:embed vcruntime140_1.dll
var embeddedLegacyExtractorVCRuntime []byte

// EnsureForVersion is retained for the legacy launchercore dependency setup.
// Its versionDir argument is launchercore's private DLL directory.
func EnsureForVersion(ctx context.Context, versionDir string) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	dir := strings.TrimSpace(versionDir)
	if dir == "" || len(embeddedLegacyExtractorVCRuntime) == 0 {
		return false
	}
	dest := filepath.Join(dir, "vcruntime140_1.dll")
	if data, err := os.ReadFile(dest); err == nil && bytes.Equal(data, embeddedLegacyExtractorVCRuntime) {
		return true
	}
	return fsx.WriteFileAtomic(dest, embeddedLegacyExtractorVCRuntime, 0o644) == nil
}
