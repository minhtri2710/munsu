// Its own module, like citations and guardsites: a requirement added under a
// dot-directory would be stripped by `go mod tidy` at the root. It requires
// nothing; `go test -json` output is parsed with encoding/json.
module munsu/skipledger

go 1.26
