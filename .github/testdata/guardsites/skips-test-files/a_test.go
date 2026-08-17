// A test file's own refusals are not production guards. Counting them would put
// t.Fatalf-adjacent branches into the baseline and hide the real ones.
package fixture

import "fmt"

func helper(n int) error {
	if n < 0 {
		return fmt.Errorf("negative")
	}
	return nil
}
