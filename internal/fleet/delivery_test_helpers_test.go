package fleet

import (
	"github.com/minhtri2710/munsu/internal/domain"
	"os"
)

func gitEnvForDir(dir string) []string { return append(os.Environ(), "GIT_CEILING_DIRECTORIES="+dir) }
func validIdentity() *domain.DeliveryIdentity {
	return &domain.DeliveryIdentity{Provider: "github", Owner: "minhtri2710", Repo: "munsu", Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42", BaseRef: "main", HeadRef: "feature/test", HeadSHA: "abc123def456abc123def456abc123def456abc1", CapturedAt: "2026-07-18T12:00:00Z"}
}
