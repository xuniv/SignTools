package builders

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/go-github/v33/github"
)

// Redact removes builder credentials if a build script accidentally prints them.
func (g *GitHub) Redact(text string) string {
	if g.data.Token != "" {
		return strings.ReplaceAll(text, g.data.Token, "[redacted]")
	}
	return text
}

func (g *SelfHosted) Redact(text string) string {
	if g.Key != "" {
		return strings.ReplaceAll(text, g.Key, "[redacted]")
	}
	return text
}

func (s *Semaphore) Redact(text string) string {
	if s.data.Token != "" {
		return strings.ReplaceAll(text, s.data.Token, "[redacted]")
	}
	return text
}

// Activity returns bounded plain text. GitHub workflow_dispatch does not provide a
// run id, and builders consume a shared queue: never label a recent run as an app's run.
func (g *GitHub) Activity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runs, _, err := g.client.Actions.ListWorkflowRunsByFileName(ctx, g.data.OrgName, g.data.RepoName, g.data.WorkflowFileName, &github.ListWorkflowRunsOptions{
		Branch: strings.TrimPrefix(g.data.Ref, "refs/heads/"), Event: "workflow_dispatch", ListOptions: github.ListOptions{PerPage: 5},
	})
	if err != nil {
		return "", fmt.Errorf("GitHub activity is unavailable; verify Actions read permission")
	}
	var out strings.Builder
	fmt.Fprintln(&out, "Recent builder workflow activity (shared queue; these runs are not correlated to this app).")
	if len(runs.WorkflowRuns) == 0 {
		fmt.Fprintln(&out, "No dispatched runs found yet.")
		return out.String(), nil
	}
	for i, run := range runs.WorkflowRuns {
		fmt.Fprintf(&out, "\nRun #%d — %s %s\n%s\n", run.GetRunNumber(), run.GetStatus(), run.GetConclusion(), run.GetHTMLURL())
		if i > 0 {
			continue
		}
		jobs, _, err := g.client.Actions.ListWorkflowJobs(ctx, g.data.OrgName, g.data.RepoName, run.GetID(), &github.ListWorkflowJobsOptions{ListOptions: github.ListOptions{PerPage: 30}})
		if err != nil {
			fmt.Fprintln(&out, "Step details unavailable with this token.")
			continue
		}
		for _, job := range jobs.Jobs {
			fmt.Fprintf(&out, "  %s: %s %s\n", job.GetName(), job.GetStatus(), job.GetConclusion())
			for _, step := range job.Steps {
				fmt.Fprintf(&out, "    %s: %s %s\n", step.GetName(), step.GetStatus(), step.GetConclusion())
			}
		}
	}
	text := out.String()
	if g.data.Token != "" {
		text = strings.ReplaceAll(text, g.data.Token, "[redacted]")
	}
	return text, nil
}

func (g *SelfHosted) Activity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, err := g.Status(ctx)
	if err != nil {
		return "", fmt.Errorf("self-hosted activity is unavailable; verify the builder URL and key")
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 256<<10))
	if err != nil && len(data) == 0 {
		return "", fmt.Errorf("no builder activity received yet")
	}
	text := string(data)
	if g.Key != "" {
		text = strings.ReplaceAll(text, g.Key, "[redacted]")
	}
	return "Current self-hosted builder activity (shared queue; may describe another app).\n\n" + text, nil
}
