package graph

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"obblog/internal/markdown"
	"obblog/internal/vault"
)

type Node struct {
	ID       string   `json:"id"`
	Path     string   `json:"path,omitempty"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Tags     []string `json:"tags,omitempty"`
	Incoming int      `json:"incoming"`
	Outgoing int      `json:"outgoing"`
	Degree   int      `json:"degree"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Count  int    `json:"count"`
}

type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type Result struct {
	Nodes    []*Node   `json:"nodes"`
	Edges    []*Edge   `json:"edges"`
	Warnings []Warning `json:"warnings"`
}

func Build(ctx context.Context, v *vault.Vault, snapshot *vault.Snapshot) (*Result, error) {
	result := &Result{Nodes: []*Node{}, Edges: []*Edge{}, Warnings: []Warning{}}
	nodes := map[string]*Node{}
	edges := map[[2]string]*Edge{}
	for _, e := range snapshot.Files {
		label := e.Name
		if e.Kind == "note" {
			label = strings.TrimSuffix(e.Name, path.Ext(e.Name))
		}
		nodes["file:"+e.Path] = &Node{ID: "file:" + e.Path, Path: e.Path, Label: label, Kind: e.Kind}
	}
	connect := func(source, target string) {
		// Heading/block links resolve to files, not separate graph nodes or self-loops.
		if source == target {
			return
		}
		key := [2]string{source, target}
		if edge := edges[key]; edge != nil {
			edge.Count++
		} else {
			edges[key] = &Edge{Source: source, Target: target, Count: 1}
		}
	}
	for _, e := range snapshot.Notes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, _, err := v.ReadNote(e.Path)
		if err != nil {
			result.Warnings = append(result.Warnings, Warning{Path: e.Path, Message: "文件未能读取或超过大小限制"})
			continue
		}
		metadata := markdown.Extract(raw)
		source := "file:" + e.Path
		nodes[source].Tags = metadata.Tags
		if metadata.InvalidYAML {
			result.Warnings = append(result.Warnings, Warning{Path: e.Path, Message: "YAML 属性无效，已跳过属性链接和标签"})
		}
		for _, tag := range metadata.Tags {
			id := "tag:" + tag
			if nodes[id] == nil {
				nodes[id] = &Node{ID: id, Label: "#" + tag, Kind: "tag"}
			}
			connect(source, id)
		}
		for _, ref := range metadata.Links {
			u, err := url.Parse(ref.Target)
			if err == nil && (u.IsAbs() || u.Host != "") {
				continue
			}
			resolve := snapshot.Resolve
			if ref.Wiki {
				resolve = snapshot.ResolveWiki
			}
			target, err := resolve(e.Path, ref.Target)
			if err == nil {
				connect(source, "file:"+target.Path)
				continue
			}
			if errors.Is(err, vault.ErrAmbiguous) {
				result.Warnings = append(result.Warnings, Warning{Path: e.Path, Message: fmt.Sprintf("链接 %q 有同名歧义，请使用完整路径", ref.Target)})
				continue
			}
			missing := path.Clean(ref.Target)
			if strings.HasPrefix(ref.Target, "./") || strings.HasPrefix(ref.Target, "../") {
				missing = path.Clean(path.Join(path.Dir(e.Path), ref.Target))
			}
			if !vault.ValidPath(missing) {
				continue
			}
			missing = strings.TrimSuffix(missing, ".md")
			id := "unresolved:" + missing
			if nodes[id] == nil {
				nodes[id] = &Node{ID: id, Label: path.Base(missing), Kind: "unresolved"}
			}
			connect(source, id)
		}
	}
	neighbors := map[string]map[string]bool{}
	for _, edge := range edges {
		result.Edges = append(result.Edges, edge)
		nodes[edge.Source].Outgoing++
		nodes[edge.Target].Incoming++
		for _, pair := range [][2]string{{edge.Source, edge.Target}, {edge.Target, edge.Source}} {
			if neighbors[pair[0]] == nil {
				neighbors[pair[0]] = map[string]bool{}
			}
			neighbors[pair[0]][pair[1]] = true
		}
	}
	for id, n := range nodes {
		n.Degree = len(neighbors[id])
		result.Nodes = append(result.Nodes, n)
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool {
		a, b := result.Edges[i], result.Edges[j]
		if a.Source == b.Source {
			return a.Target < b.Target
		}
		return a.Source < b.Source
	})
	return result, nil
}
