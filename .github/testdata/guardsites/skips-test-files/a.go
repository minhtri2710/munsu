package fixture

import "fmt"

func Production(n int) error {
	if n < 0 {
		return fmt.Errorf("negative")
	}
	return nil
}
