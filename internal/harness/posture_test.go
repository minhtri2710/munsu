package harness

import "testing"

func TestPostureOf(t *testing.T) {
	deny := Adapters[Pi].QuestionDeny
	tests := []struct {
		name    string
		harness string
		args    []string
		want    LaunchPosture
	}{
		{"pi argv carrying the deny", Pi, append([]string{"--model", "m"}, append(append([]string{}, deny...), "prompt")...), LaunchPosture{QuestionDeny: true}},
		{"pi argv ending with the deny", Pi, append([]string{"--model", "m"}, deny...), LaunchPosture{QuestionDeny: true}},
		{"pi argv without the deny", Pi, []string{"--model", "m", "prompt"}, LaunchPosture{}},
		{"pi argv with only the flag", Pi, []string{deny[0], "other"}, LaunchPosture{}},
		{"pi argv with the pair split apart", Pi, []string{deny[0], "x", deny[1]}, LaunchPosture{}},
		{"pi argv with the pair in reverse", Pi, []string{deny[1], deny[0]}, LaunchPosture{}},
		{"pi nil argv", Pi, nil, LaunchPosture{}},
		{"claude argv with both denies", Claude, []string{"--disallowedTools", "AskUserQuestion", "--disallowedTools", "Skill(munsu-ops)", "--", "prompt"}, LaunchPosture{QuestionDeny: true, SkillBlock: true}},
		{"claude argv with only the question deny", Claude, []string{"--disallowedTools", "AskUserQuestion", "--", "prompt"}, LaunchPosture{QuestionDeny: true}},
		{"harness with no expressible deny", Codex, []string{"--exclude-tools", "ask_user_question"}, LaunchPosture{}},
		{"unknown harness", "nonexistent", deny, LaunchPosture{}},
	}
	for _, tt := range tests {
		got := PostureOf(tt.harness, tt.args)
		if got != tt.want {
			t.Errorf("%s: PostureOf(%q) = %+v, want %+v", tt.name, tt.harness, got, tt.want)
		}
	}
}
