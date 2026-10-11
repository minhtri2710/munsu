package fence

// Supported returns nil: macOS has a write fence.
func Supported() error { return nil }
