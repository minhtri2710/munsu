package home

// DurableKey returns the persisted file stem (no extension) for a logical
// task key (id). Every id that passes validateTaskID is already a safe
// filename, so the durable key is the identity.
func DurableKey(id string) (string, error) {
	if err := validateTaskID(id); err != nil {
		return "", err
	}
	return id, nil
}

// ReverseDurableKey returns the logical task key for a persisted file stem.
// The stem is already the logical key, so only the logical id is validated.
func ReverseDurableKey(stem string) (string, error) {
	if err := validateTaskID(stem); err != nil {
		return "", err
	}
	return stem, nil
}
