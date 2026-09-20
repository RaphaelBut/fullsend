package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/fullsend-ai/fullsend/internal/forge"
	gl "github.com/fullsend-ai/fullsend/internal/forge/gitlab"
	"github.com/fullsend-ai/fullsend/internal/gitlabroles"
	"github.com/fullsend-ai/fullsend/internal/repos"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// Diagnostic env vars published after GitLab role selection. They carry
// names and policy labels, never token values.
const (
	envGitLabRole       = "FULLSEND_GITLAB_ROLE"
	envGitLabRoleSecret = "FULLSEND_GITLAB_ROLE_SECRET"
	envGitLabRoleSource = "FULLSEND_GITLAB_ROLE_SOURCE"
)

func resolveGitLabPollerCredential(getenv func(string) string) (gitlabroles.Selection, string, error) {
	sel, err := gitlabroles.Select(gitlabroles.PollerJob(), getenv)
	if err != nil {
		return sel, "", err
	}
	token, err := sel.Token(getenv)
	if err != nil {
		return sel, "", err
	}
	return sel, token, nil
}

func resolveGitLabAgentCredential(agentName, harnessRole string, getenv func(string) string) (gitlabroles.Selection, string, error) {
	sel, err := gitlabroles.SelectAgent(agentName, harnessRole, getenv)
	if err != nil {
		return sel, "", err
	}
	token, err := sel.Token(getenv)
	if err != nil {
		return sel, "", err
	}
	return sel, token, nil
}

func applyGitLabAgentCredentials(agentName, harnessRole string, getenv func(string) string, setenv func(string, string), printer *ui.Printer) error {
	sel, token, err := resolveGitLabAgentCredential(agentName, harnessRole, getenv)
	if err != nil {
		return err
	}
	applyGitLabRoleSelection(sel, token, setenv, printer)
	return nil
}

func applyGitLabRoleSelection(sel gitlabroles.Selection, token string, setenv func(string, string), printer *ui.Printer) {
	if printer != nil {
		for _, line := range sel.Diagnostics() {
			printer.StepInfo(line)
		}
	}
	if setenv == nil {
		setenv = func(k, v string) { _ = os.Setenv(k, v) }
	}
	setenv("GITLAB_TOKEN", token)
	setenv(envGitLabRole, string(sel.Source.Role))
	setenv(envGitLabRoleSecret, sel.Source.SecretName)
	setenv(envGitLabRoleSource, sel.IdentitySource())
	if sel.Mode.UsesSharedOnly() {
		return
	}
	if sel.Registration.Has(gitlabroles.CapWriteRepository) {
		setenv("PUSH_TOKEN", token)
		setenv("PUSH_TOKEN_SOURCE", "pat")
		return
	}
	// Analyst, Poller, and custom roles without write_repository must
	// not inherit the shared PUSH_TOKEN exported by CI templates.
	setenv("PUSH_TOKEN", "")
}

func logGitLabRoleDiagnostics(sel gitlabroles.Selection, printer *ui.Printer) {
	if printer == nil {
		return
	}
	for _, line := range sel.Diagnostics() {
		printer.StepInfo(line)
	}
}

func isGitLabAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	if forge.IsForbidden(err) {
		return true
	}
	var api *gl.APIError
	if errors.As(err, &api) && api != nil {
		return api.StatusCode == http.StatusUnauthorized || api.StatusCode == http.StatusForbidden
	}
	return false
}

func wrapGitLabAuthFailure(sel gitlabroles.Selection, err error) error {
	if err == nil || !isGitLabAuthFailure(err) {
		return err
	}
	return fmt.Errorf("%w: %w", gitlabroles.AuthFailed(sel.Source.Role, sel.Mode, sel.Source.SecretName), err)
}

// checkGitLabApprovalCapability rejects GitLab APPROVE reviews when the
// running identity does not declare approve_merge_request. Disabled and
// rollback keep the shared-token path so existing installations are
// unchanged. getenv nil means os.Getenv.
func checkGitLabApprovalCapability(forgeName, action string, getenv func(string) string) error {
	if forgeName != repos.ForgeGitLab {
		return nil
	}
	event, ok := reviewActionToEvent(action)
	if !ok || event != "APPROVE" {
		return nil
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	mode, err := gitlabroles.ModeFrom(getenv)
	if err != nil {
		return err
	}
	if mode.UsesSharedOnly() {
		return nil
	}
	agentName := strings.TrimSpace(getenv(envGitLabRole))
	if agentName == "" {
		agentName = strings.TrimSpace(getenv("STAGE"))
	}
	harnessRole := strings.TrimSpace(getenv(envGitLabRole))
	sel, err := gitlabroles.SelectAgent(agentName, harnessRole, getenv)
	if err != nil {
		return err
	}
	return gitlabroles.Require(sel.Registration, gitlabroles.CapApproveMergeRequest)
}
