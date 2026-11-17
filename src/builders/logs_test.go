package builders

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGitHubActivityIsScopedAndHonestAboutSharedQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/ci/actions/workflows/sign.yml/runs":
			if r.URL.Query().Get("branch") != "master" || r.URL.Query().Get("event") != "workflow_dispatch" {
				t.Errorf("unscoped activity request: %s", r.URL)
			}
			fmt.Fprint(w, `{"total_count":1,"workflow_runs":[{"id":17,"run_number":4,"status":"in_progress","html_url":"https://github.com/owner/ci/actions/runs/17"}]}`)
		case "/repos/owner/ci/actions/runs/17/jobs":
			fmt.Fprint(w, `{"total_count":1,"jobs":[{"name":"Sign","status":"in_progress","steps":[{"name":"token-secret","status":"completed","conclusion":"success"}]}]}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	g := MakeGitHub(&GitHubData{OrgName: "owner", RepoName: "ci", WorkflowFileName: "sign.yml", Ref: "refs/heads/master", Token: "token-secret"})
	g.client.BaseURL, _ = url.Parse(server.URL + "/")
	text, err := g.Activity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "not correlated to this app") || !strings.Contains(text, "Sign: in_progress") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("missing activity context or redaction: %s", text)
	}
	if strings.Contains(text, "token-secret") {
		t.Fatal("builder token appeared in activity")
	}
}

func TestSelfHostedActivityKeepsKeyServerSide(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer builder-key" {
			t.Error("builder key missing")
		}
		fmt.Fprint(w, "signing builder-key\n<script>alert(1)</script>")
	}))
	defer server.Close()
	g := MakeSelfHosted(&SelfHostedData{Url: server.URL, Key: "builder-key"})
	text, err := g.Activity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "builder-key") || !strings.Contains(text, "[redacted]") || !strings.Contains(text, "shared queue") {
		t.Fatalf("key leaked or scope missing: %s", text)
	}
}
