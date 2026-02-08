package version

// Version is the current version of the axon binary. It is set at build time
// via ldflags. When unset (e.g. during development) it defaults to "latest".
var Version = "latest"
