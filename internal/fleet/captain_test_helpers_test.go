package fleet

func safeStr(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
