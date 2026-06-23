// Package identity auto-detects the producing repository and build so that
// every artifact is self-identifying without any action from the test author.
// Detection prefers explicit PHROND_* overrides, then well-known CI
// environment variables, and finally falls back to the local git checkout.
package identity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/phrond/evermock/internal/artifact"
)

// Detect resolves producer identity from the environment and local git.
func Detect() artifact.Producer {
	if p, ok := fromOverrides(); ok {
		return p
	}
	if p, ok := fromGitHubActions(); ok {
		return p
	}
	if p, ok := fromGitLab(); ok {
		return p
	}
	if p, ok := fromGit(); ok {
		return p
	}
	return artifact.Producer{}
}

func fromOverrides() (artifact.Producer, bool) {
	repo := env("PHROND_REPO")
	sha := env("PHROND_COMMIT_SHA")
	if repo == "" && sha == "" {
		return artifact.Producer{}, false
	}
	return artifact.Producer{
		Repo:      repo,
		CommitSHA: sha,
		Branch:    env("PHROND_BRANCH"),
		Build:     env("PHROND_BUILD"),
		Source:    "env",
	}, true
}

func fromGitHubActions() (artifact.Producer, bool) {
	repo := env("GITHUB_REPOSITORY")
	sha := env("GITHUB_SHA")
	if repo == "" && sha == "" {
		return artifact.Producer{}, false
	}
	return artifact.Producer{
		Repo:      repo,
		CommitSHA: sha,
		Branch:    env("GITHUB_REF_NAME"),
		Build:     env("GITHUB_RUN_ID"),
		Source:    "github-actions",
	}, true
}

func fromGitLab() (artifact.Producer, bool) {
	repo := env("CI_PROJECT_PATH")
	sha := env("CI_COMMIT_SHA")
	if repo == "" && sha == "" {
		return artifact.Producer{}, false
	}
	return artifact.Producer{
		Repo:      repo,
		CommitSHA: sha,
		Branch:    env("CI_COMMIT_REF_NAME"),
		Build:     env("CI_PIPELINE_ID"),
		Source:    "gitlab-ci",
	}, true
}

func fromGit() (artifact.Producer, bool) {
	sha := gitOutput("rev-parse", "HEAD")
	if sha == "" {
		return artifact.Producer{}, false
	}
	return artifact.Producer{
		Repo:      repoNameFromGit(),
		CommitSHA: sha,
		Branch:    gitOutput("rev-parse", "--abbrev-ref", "HEAD"),
		Source:    "git",
	}, true
}

// repoNameFromGit returns an "owner/repo" style name from the origin remote,
// falling back to the working-tree directory name.
func repoNameFromGit() string {
	if remote := gitOutput("config", "--get", "remote.origin.url"); remote != "" {
		return repoNameFromRemote(remote)
	}
	if top := gitOutput("rev-parse", "--show-toplevel"); top != "" {
		return filepath.Base(top)
	}
	return ""
}

func repoNameFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	// Normalize scp-style (git@host:owner/repo) and URL forms to a path tail.
	if idx := strings.LastIndex(remote, ":"); idx != -1 && !strings.Contains(remote[idx:], "/") {
		remote = remote[idx+1:]
	} else if idx := strings.Index(remote, "://"); idx != -1 {
		rest := remote[idx+3:]
		if slash := strings.Index(rest, "/"); slash != -1 {
			remote = rest[slash+1:]
		}
	}
	remote = strings.Trim(remote, "/")
	parts := strings.Split(remote, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return remote
}

func gitOutput(args ...string) string {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func env(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
