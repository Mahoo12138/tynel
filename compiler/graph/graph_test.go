package graph

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/project"
)

type fakeScanner map[string][]Import

func (f fakeScanner) Imports(file project.File) ([]Import, error) {
	return f[file.Path], nil
}

func setup(t *testing.T, files map[string]string) (*project.Root, project.File) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := project.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := root.ResolveEntry("main.ts")
	if err != nil {
		t.Fatal(err)
	}
	return root, entry
}

func codeOf(err error) string {
	var d *diagnostic.Diagnostic
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func TestLoadLinear(t *testing.T) {
	root, entry := setup(t, map[string]string{
		"main.ts": "import { a } from './a.ts';",
		"a.ts":    "import { b } from './b.ts';",
		"b.ts":    "export const b = 1;",
	})
	scan := fakeScanner{
		"main.ts": {{Specifier: "./a.ts"}},
		"a.ts":    {{Specifier: "./b.ts"}},
	}
	g, err := Load(root, entry, scan)
	if err != nil {
		t.Fatalf("线性依赖载入失败：%v", err)
	}
	if len(g.Files) != 3 || len(g.Edges) != 2 {
		t.Fatalf("文件 %d 条、边 %d 条，期望 3 与 2", len(g.Files), len(g.Edges))
	}
	if g.Files[0].Path != "main.ts" || g.Files[2].Path != "b.ts" {
		t.Fatalf("发现顺序不对：%+v", g.Files)
	}
}

func TestLoadDiamond(t *testing.T) {
	root, entry := setup(t, map[string]string{
		"main.ts":   "import { a } from './a.ts'; import { b } from './b.ts';",
		"a.ts":      "import { shared } from './shared.ts';",
		"b.ts":      "import { shared } from './shared.ts';",
		"shared.ts": "export const shared = 1;",
	})
	scan := fakeScanner{
		"main.ts":   {{Specifier: "./a.ts"}, {Specifier: "./b.ts"}},
		"a.ts":      {{Specifier: "./shared.ts"}},
		"b.ts":      {{Specifier: "./shared.ts"}},
		"shared.ts": nil,
	}
	g, err := Load(root, entry, scan)
	if err != nil {
		t.Fatalf("菱形依赖载入失败：%v", err)
	}
	if len(g.Files) != 4 || len(g.Edges) != 4 {
		t.Fatalf("文件 %d 条、边 %d 条，期望 4 与 4", len(g.Files), len(g.Edges))
	}
	count := 0
	for _, file := range g.Files {
		if file.Path == "shared.ts" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared.ts 出现了 %d 次", count)
	}
	if g.Hash == "" {
		t.Fatal("图缺少哈希")
	}
}

func TestLoadTypeOnlyEdge(t *testing.T) {
	root, entry := setup(t, map[string]string{
		"main.ts":  "import type { T } from './types.ts';",
		"types.ts": "export type T = number;",
	})
	scan := fakeScanner{"main.ts": {{Specifier: "./types.ts", TypeOnly: true}}}
	g, err := Load(root, entry, scan)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 || !g.Edges[0].TypeOnly {
		t.Fatalf("类型边没有保留：%+v", g.Edges)
	}
}

func TestLoadRejectsCycle(t *testing.T) {
	root, entry := setup(t, map[string]string{
		"main.ts": "import { a } from './a.ts';",
		"a.ts":    "import { b } from './b.ts';",
		"b.ts":    "import { a } from './a.ts';",
	})
	scan := fakeScanner{
		"main.ts": {{Specifier: "./a.ts"}},
		"a.ts":    {{Specifier: "./b.ts"}},
		"b.ts":    {{Specifier: "./a.ts"}},
	}
	if _, err := Load(root, entry, scan); codeOf(err) != "graph.cycle" {
		t.Fatalf("应拒绝依赖环，实际 %v", err)
	}
}

func TestLoadRejectsMissing(t *testing.T) {
	root, entry := setup(t, map[string]string{"main.ts": "import './missing.ts';"})
	scan := fakeScanner{"main.ts": {{Specifier: "./missing.ts"}}}
	if _, err := Load(root, entry, scan); codeOf(err) != "project.missing" {
		t.Fatalf("应拒绝缺失目标，实际 %v", err)
	}
}

func TestLoadRejectsResolutionMismatch(t *testing.T) {
	root, entry := setup(t, map[string]string{
		"main.ts": "import './a.ts';",
		"a.ts":    "export const a = 1;",
	})
	scan := fakeScanner{"main.ts": {{Specifier: "./a.ts", Resolved: "/tmp/elsewhere.ts"}}}
	if _, err := Load(root, entry, scan); codeOf(err) != "graph.resolution-mismatch" {
		t.Fatalf("应拒绝解析不一致，实际 %v", err)
	}
}

func TestReportHasNoBytes(t *testing.T) {
	root, entry := setup(t, map[string]string{"main.ts": "console.log(1);"})
	g, err := Load(root, entry, fakeScanner{})
	if err != nil {
		t.Fatal(err)
	}
	report := g.Report()
	if report.Entry != "main.ts" || len(report.Files) != 1 || report.Hash == "" {
		t.Fatalf("报告不对：%+v", report)
	}
}
