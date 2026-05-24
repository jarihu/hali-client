package buildinfo

// Set via -ldflags at build time. Do not branch runtime behavior on these values.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildMode = "debug"
	Edition   = "oss"
)
