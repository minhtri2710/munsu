package fence

import "os"

// unsafeDir is "" on windows: it has no POSIX owner or mode, and the fence
// itself is unsupported there (Supported reports it).
func unsafeDir(os.FileInfo) string { return "" }
