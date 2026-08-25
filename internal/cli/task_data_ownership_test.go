package cli

import "testing"

func TestTaskDataDirReclaimerFailsClosedWithoutAnAuthority(t *testing.T) {
	reclaim := taskDataDirReclaimer(t.TempDir())
	called := false
	reclaimed, err := reclaim("any-task", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed || called {
		t.Fatal("an unopenable home must not reclaim task data")
	}
}
