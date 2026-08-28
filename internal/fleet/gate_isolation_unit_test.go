//go:build !integration

package fleet

func setupFleetTestFixtures() (func(), error) {
	return func() {}, nil
}
