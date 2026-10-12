package harness

import "os"

// ownedByCurrentUser is true on windows: it has no POSIX owner, and the fence
// that keeps seats out of the seat build dir is unsupported there.
func ownedByCurrentUser(os.FileInfo) bool { return true }
