// The guards lane's measuring instrument is its own module on purpose. It needs
// golang.org/x/tools to type-check the tree; the shipped munsu module does not,
// and `go mod tidy` at the root cannot see this directory at all (the go tool
// ignores paths under a dot-directory), so a requirement added there would be
// stripped by the next tidy and take the lane down silently.
module munsu/guardsites

go 1.26

require golang.org/x/tools v0.49.0

require (
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)
