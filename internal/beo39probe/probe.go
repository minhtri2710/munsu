// Package beo39probe exists only on the throwaway BEO-39 probe branch. It is
// deliberately not gofmt-clean so the `Repo invariants` lane fails while the
// other three required lanes stay green. Never merge this into main.
package beo39probe

type probe struct {
	a string
	bb  int
}

var _ = probe{}
