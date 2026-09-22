package install

import (
	"context"
	"fmt"
	"os"

	"github.com/fullsend-ai/fullsend/internal/e2etest"
	"github.com/fullsend-ai/fullsend/internal/forge"
)

// actorGrant is a direct collaborator grant for a human-like test actor.
type actorGrant struct {
	login      string
	permission string
}

// actorGrantEnv maps each actor PAT to the permission its account holds on
// pool repos (docs/guides/dev/e2e-testing.md, "Test actor permissions").
// The outsider is deliberately absent: it must stay a non-collaborator.
var actorGrantEnv = []struct{ patEnv, permission string }{
	{"TEST_ACTOR_WRITE_PAT", "push"},
	{"TEST_ACTOR_TRIAGE_PAT", "triage"},
}

// actorGrantsFromEnv resolves the login behind each actor PAT that is set.
// An actor whose login cannot be resolved is logged and skipped.
func actorGrantsFromEnv(ctx context.Context, logf func(string, ...any)) []actorGrant {
	var grants []actorGrant
	for _, a := range actorGrantEnv {
		pat := os.Getenv(a.patEnv)
		if pat == "" {
			continue
		}
		login, err := e2etest.NewLiveClient(pat).GetAuthenticatedUser(ctx)
		if err != nil {
			logf("[ensure] skipping %s grant: resolving login: %v", a.patEnv, err)
			continue
		}
		grants = append(grants, actorGrant{login: login, permission: a.permission})
	}
	return grants
}

// grantActors re-applies the actor grants. resetRepo deletes the repo,
// and direct collaborator grants are deleted with it.
func (e *repoEnsurer) grantActors(ctx context.Context, org, repoName string) error {
	if len(e.actorGrants) == 0 {
		return nil
	}
	gh, ok := e.client.(forge.GitHubExtensions)
	if !ok {
		return fmt.Errorf("granting test actors on %s/%s: forge client has no collaborator API", org, repoName)
	}
	for _, g := range e.actorGrants {
		if err := gh.AddCollaborator(ctx, org, repoName, g.login, g.permission); err != nil {
			return fmt.Errorf("granting %s %s on %s/%s: %w", g.login, g.permission, org, repoName, err)
		}
		e.logf("[ensure] granted %s %s on %s/%s", g.login, g.permission, org, repoName)
	}
	return nil
}
