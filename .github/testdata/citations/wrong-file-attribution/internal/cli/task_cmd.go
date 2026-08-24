package cli

func runTask(home string) error {
	_, err := resolveCurrentTaskID(home)
	return err
}
