package buildinfo

// version is set with -ldflags "-X centavo/partner/internal/platform/buildinfo.version=...".
var version = "dev"

func Version() string {
	return version
}
