package mailbox

type BoundSendResult struct {
	Status       string
	Detail       string
	Acknowledged bool
	Err          error
}

type BoundSender interface {
	Alive(homeDir string, meta map[string]string) (bool, error)
	Send(homeDir string, meta map[string]string, payload string) BoundSendResult
}
