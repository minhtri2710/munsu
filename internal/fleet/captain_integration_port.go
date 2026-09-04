package fleet

type IntegrationStatus struct {
	Harness string
	Scope   string
	State   string
	Message string
}

type IntegrationPort interface {
	EnsureCaptain(home, harness string) error
	Status(home, harness string) (IntegrationStatus, error)
}

type CaptainWorktreeSeedOptions struct {
	ID, Home, Repo, ParentHome, Charter, Ref string
	Force                                    bool
	Integration                              IntegrationPort
}
type CaptainMigrationOptions struct {
	CaptainHome, Repo, ID, ParentHome string
	Integration                       IntegrationPort
}
