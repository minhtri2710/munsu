// The citation lane's instrument is its own module, like the guards lane's
// (.github/scripts/guardsites/go.mod): `go mod tidy` at the root cannot see a
// directory under a dot-directory, so a requirement added there would be
// stripped by the next tidy and take the lane down silently.
//
// Unlike that one, this module requires nothing. Everything it needs -- a Go
// parser and a filesystem walk -- is in the standard library, so `go run .`
// here touches no module cache and no network. That is not an accident of
// scope: this tool runs in the `invariants` job, which is trusted precisely
// because nothing outside the checkout can change what it says. Do not add a
// dependency without moving that argument somewhere else first.
module munsu/citations

go 1.26
