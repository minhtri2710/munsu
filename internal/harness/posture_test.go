package harness

import "testing"

func TestPostureOf(t *testing.T) {
	deny := Adapters[Pi].QuestionDeny
	tests := []struct {
		name    string
		harness string
		args    []string
		want    bool
	}{
		{"pi argv carrying the deny", Pi, append([]string{"--model", "m"}, append(append([]string{}, deny...), "prompt")...), true},
		{"pi argv ending with the deny", Pi, append([]string{"--model", "m"}, deny...), true},
		{"pi argv without the deny", Pi, []string{"--model", "m", "prompt"}, false},
		{"pi argv with only the flag", Pi, []string{deny[0], "other"}, false},
		{"pi argv with the pair split apart", Pi, []string{deny[0], "x", deny[1]}, false},
		{"pi argv with the pair in reverse", Pi, []string{deny[1], deny[0]}, false},
		{"pi nil argv", Pi, nil, false},
		{"harness with no expressible deny", Claude, []string{"--exclude-tools", "ask_user_question"}, false},
		{"unknown harness", "nonexistent", deny, false},
	}
	for _, tt := range tests {
		got := PostureOf(tt.harness, tt.args)
		if got.QuestionDeny != tt.want {
			t.Errorf("%s: PostureOf(%q).QuestionDeny = %v, want %v", tt.name, tt.harness, got.QuestionDeny, tt.want)
		}
		if got.SkillBlock {
			t.Errorf("%s: SkillBlock = true; argv cannot show it, only the launch sets it", tt.name)
		}
	}
}
