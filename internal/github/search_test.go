package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestMyPRs(t *testing.T) {
	t.Parallel()

	rec := &gqlRecorder{body: readFixture(t, "search.json")}
	c, _ := newTestClient(t, "github.com", rec)

	got, err := c.MyPRs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var body struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal([]byte(rec.requests()[0]), &body); err != nil {
		t.Fatal(err)
	}

	wantVars := map[string]any{
		"requested": "is:open is:pr review-requested:@me",
		"authored":  "is:open is:pr author:@me",
	}
	if !reflect.DeepEqual(body.Variables, wantVars) {
		t.Errorf("variables = %v, want %v", body.Variables, wantVars)
	}

	type brief struct {
		Repo   Repo
		Number int
		Author string
	}

	briefs := func(in []SearchPR) []brief {
		var out []brief
		for _, s := range in {
			out = append(out, brief{s.Repo, s.PR.Number, s.PR.Author})
		}

		return out
	}

	wantReq := []brief{
		{Repo{"github.com", "acme", "web"}, 311, "kari-dev"},
		{Repo{"github.com", "acme", "web"}, 305, "lars"},
		{Repo{"github.com", "acme", "infra"}, 88, "bot-user"},
	}
	if g := briefs(got.Requested); !reflect.DeepEqual(g, wantReq) {
		t.Fatalf("Requested = %v, want %v", g, wantReq)
	}

	if c := got.Requested[0].PR.Checks; c.Total != 6 || c.Done != 6 || c.State != CheckSuccess {
		t.Errorf("checks of #311 = %+v, want 6 of 6", c)
	}

	wantAuth := []brief{{Repo{"github.com", "mhersson", "muxreveal"}, 24, "mhersson"}}
	if g := briefs(got.Authored); !reflect.DeepEqual(g, wantAuth) {
		t.Errorf("Authored = %v, want %v", g, wantAuth)
	}

	if !got.Authored[0].PR.ViewerAuthored {
		t.Error("ViewerAuthored = false on an authored pull request")
	}
}

func TestMyPRsGHE(t *testing.T) {
	t.Parallel()

	rec := &gqlRecorder{body: readFixture(t, "search.json")}
	c, reqs := newTestClient(t, "ghe.example.com", rec)

	got, err := c.MyPRs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if r := reqs(); len(r) != 1 || r[0].URL.String() != "https://ghe.example.com/api/graphql" {
		t.Fatalf("requests = %v, want one to https://ghe.example.com/api/graphql", r)
	}

	for _, s := range append(got.Requested, got.Authored...) {
		if s.Repo.Host != "ghe.example.com" {
			t.Errorf("repo host = %q, want ghe.example.com", s.Repo.Host)
		}
	}
}

func TestMyPRsError(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"boom"}`)
	}))

	_, err := c.MyPRs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "github: query MyPRs") {
		t.Fatalf("err = %v, want one naming github: query MyPRs", err)
	}
}
