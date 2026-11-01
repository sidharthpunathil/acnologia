package providers

import (
	"embed"
	"path"
)

//go:embed seedlogos
var seedLogos embed.FS

// SeedLogo returns the logo shipped for a built-in provider, if any.
func SeedLogo(slug string) ([]byte, string) {
	for _, ext := range []string{".png", ".svg"} {
		if b, err := seedLogos.ReadFile(path.Join("seedlogos", slug+ext)); err == nil {
			if ext == ".svg" {
				return b, "image/svg+xml"
			}
			return b, "image/png"
		}
	}
	return nil, ""
}
