package captain

type NudgeResult struct {
	Status       string
	Detail       string
	Acknowledged bool
}

type NudgeEndpoint interface {
	Nudge(home string, meta map[string]string, payload string) (NudgeResult, error)
}
