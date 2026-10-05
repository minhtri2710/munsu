package harness

// LaunchPosture is what a soldier launch argv guarantees, read as typed
// fields so a caller never parses argv itself.
type LaunchPosture struct {
	// QuestionDeny is true when the argv carries the adapter's question deny.
	QuestionDeny bool
	// SkillBlock is true only when the orchestration skill is blocked for this
	// launch: by the argv's skill deny, or, for a harness that filters skills
	// through files, by the launch once it wrote both levels (the user-level
	// filtered agent dir and the worktree project settings).
	SkillBlock bool
}

// PostureOf reads the posture of a launch argv built for the named harness.
// An unknown harness, or one whose deny is not expressible, has none.
func PostureOf(harnessName string, args []string) LaunchPosture {
	a, ok := GetAdapter(harnessName)
	return LaunchPosture{
		QuestionDeny: ok && containsRun(args, a.QuestionDeny),
		SkillBlock:   ok && containsRun(args, a.SoldierLaunch.SkillDeny),
	}
}

// containsRun reports whether run appears as consecutive elements of args.
func containsRun(args, run []string) bool {
	if len(run) == 0 {
		return false
	}
	for i := 0; i+len(run) <= len(args); i++ {
		match := true
		for j, r := range run {
			if args[i+j] != r {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
