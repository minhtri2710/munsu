package fleet

type IntegrationStatus struct {
	Harness string
	Scope   string
	State   string
	Message string
}

type IntegrationPort interface {
	EnsureCaptain(home string) error
	Status(home, harness string) (IntegrationStatus, error)
}
