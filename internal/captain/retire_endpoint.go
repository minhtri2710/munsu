package captain

type RetireEndpoint interface {
	Retire(home string, meta map[string]string) error
}
