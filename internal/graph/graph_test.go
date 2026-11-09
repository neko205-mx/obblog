package graph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"obblog/internal/vault"
)

func buildFixture(t *testing.T) (*vault.Vault, *vault.Snapshot) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"A.md":        "---\ntags: [topic]\nrelated: '[[folder/B]]'\n---\n[[folder/B#heading]] [[folder/B#^block]] ![[photo.png]] [[Missing]] [[Missing.md#x]] [[A#self]] [[.secret]]\n`[[Code]]`\n%% [[Comment]] %%\n",
		"folder/B.md": "[back](../A.md) #topic\n", "Orphan.md": "no links", "photo.png": "image",
		".hidden.md": "[[Hidden]]", ".git/private.md": "[[Private]]",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	s, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	return v, s
}

func TestBuild(t *testing.T) {
	v, snapshot := buildFixture(t)
	result, err := Build(context.Background(), v, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 6 || len(result.Edges) != 6 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected graph: %+v", result)
	}
	nodes := map[string]*Node{}
	for _, node := range result.Nodes {
		nodes[node.ID] = node
	}
	if nodes["file:Orphan.md"].Degree != 0 || nodes["file:A.md"].Degree != 4 || nodes["file:folder/B.md"].Incoming != 1 || nodes["tag:topic"].Incoming != 2 {
		t.Fatalf("incorrect node degrees: %+v", nodes)
	}
	for _, edge := range result.Edges {
		if edge.Source == edge.Target {
			t.Fatal("self links should not create graph loops")
		}
		if edge.Source == "file:A.md" && edge.Target == "file:folder/B.md" && edge.Count != 3 {
			t.Fatalf("header/block/property links should aggregate: %+v", edge)
		}
		if edge.Target == "unresolved:Missing" && edge.Count != 2 {
			t.Fatalf("unresolved paths should aggregate: %+v", edge)
		}
	}
	again, err := Build(context.Background(), v, snapshot)
	if err != nil || !reflect.DeepEqual(result, again) {
		t.Fatal("graph output should be deterministic")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, v, snapshot); err == nil {
		t.Fatal("canceled requests must stop indexing")
	}
}

func TestGraphWarnings(t *testing.T) {
	v, _ := buildFixture(t)
	for name, body := range map[string]string{"bad.md": "---\ntags: [bad\n---\n[[B]]", "other/B.md": ""} {
		full := filepath.Join(v.Path, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(context.Background(), v, s)
	if err != nil || len(g.Warnings) != 2 {
		t.Fatalf("expected invalid YAML and ambiguous link warnings: %+v, %v", g, err)
	}
}

// The real vault remains opt-in and never becomes a committed test fixture.
func TestRealVault(t *testing.T) {
	dir := os.Getenv("OBBLOG_TEST_VAULT")
	if dir == "" {
		t.Skip("set OBBLOG_TEST_VAULT to a local vault for read-only integration testing")
	}
	v, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	snapshot, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][32]byte{}
	for _, n := range snapshot.Notes {
		raw, _, err := v.ReadNote(n.Path)
		if err != nil {
			t.Fatal(err)
		}
		before[n.Path] = sha256.Sum256(raw)
	}
	start := time.Now()
	g, err := Build(context.Background(), v, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		if ids[n.ID] || n.Kind == "note" && !vault.ValidPath(n.Path) {
			t.Fatal("invalid or duplicate graph node")
		}
		ids[n.ID] = true
		kinds[n.Kind]++
	}
	for _, e := range g.Edges {
		if !ids[e.Source] || !ids[e.Target] || e.Count < 1 || e.Source == e.Target {
			t.Fatal("invalid graph edge")
		}
	}
	if kinds["note"] != len(snapshot.Notes) || kinds["asset"] != snapshot.Attachments {
		t.Fatal("graph and directory snapshots disagree")
	}
	for name, hash := range before {
		raw, _, err := v.ReadNote(name)
		if err != nil || sha256.Sum256(raw) != hash {
			t.Fatal("source notes changed during graph construction")
		}
	}
	if _, err := json.Marshal(g); err != nil {
		t.Fatal(err)
	}
	after, err := v.Scan()
	if err != nil || !reflect.DeepEqual(snapshot, after) {
		t.Fatalf("vault directory snapshot changed: %v", err)
	}
	t.Logf("read-only integration: %d notes, %d attachments, %d tags, %d unresolved nodes, %d directed edges, %d warnings; index %s", kinds["note"], kinds["asset"], kinds["tag"], kinds["unresolved"], len(g.Edges), len(g.Warnings), time.Since(start).Round(time.Millisecond))
}
