// Package graph 在项目根下递归发现显式相对导入，固定每个文件的字节快照，
// 并在进入检查和降低之前拒绝缺失目标与依赖环。
//
// 图只认 tsgo 给出的导入声明：调用方通过 Scanner 提供每条导入的 specifier、
// 位置和 type-only 标记。路径解析仍由 project 负责，所以裸模块、越界和大小写
// 歧义会在同一处失败。图不执行 TypeScript，也不按正则扫描源码。
package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/project"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// Import 是扫描器返回的一条导入声明。
type Import struct {
	Specifier string
	Start     int
	Length    int
	TypeOnly  bool
	Resolved  string // tsgo 解析出的绝对路径；未知时为空
}

// Scanner 读取一个文件的导入声明。实现必须使用 TypeScript 语法树，
// 不能用正则或字符串搜索代替。
type Scanner interface {
	Imports(file project.File) ([]Import, error)
}

// Edge 是图中的一条有向边。TypeOnly 为真时，这条边只用于类型空间，
// 不产生运行时值。
type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Specifier string `json:"specifier"`
	TypeOnly  bool   `json:"typeOnly"`
	Start     int    `json:"start"`
	Length    int    `json:"length"`
}

// FileInfo 是报告里的文件身份，不包含源字节。
type FileInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Graph 是一次依赖载入的结果。Files 按发现顺序排列，Entry 是第一项；
// 同一个文件只出现一次。
type Graph struct {
	Entry project.File
	Files []project.File
	Edges []Edge
	Hash  string
}

// Report 是给命令行输出的稳定形状，不含源字节。
type Report struct {
	Entry string     `json:"entry"`
	Files []FileInfo `json:"files"`
	Edges []Edge     `json:"edges"`
	Hash  string     `json:"hash"`
}

// Load 从 entry 出发递归载入依赖。遇到缺失目标、越界路径或环时返回错误，
// 不返回半个图。
func Load(root *project.Root, entry project.File, scan Scanner) (*Graph, error) {
	if root == nil || scan == nil {
		return nil, diagnostic.New(diagnostic.Internal, "graph.missing-input", "依赖图缺少项目根或扫描器")
	}
	g := &Graph{Entry: entry}
	state := make(map[string]int) // 0 未访问，1 正在访问，2 完成
	var stack []string

	var visit func(project.File) error
	visit = func(file project.File) error {
		switch state[file.Path] {
		case 1:
			return diagnostic.New(diagnostic.Source, "graph.cycle", "依赖环：%s", cycleText(stack, file.Path))
		case 2:
			return nil
		}
		state[file.Path] = 1
		stack = append(stack, file.Path)
		g.Files = append(g.Files, file)

		imports, err := scan.Imports(file)
		if err != nil {
			return err
		}
		for _, imp := range imports {
			target, err := root.ResolveImport(file, imp.Specifier)
			if err != nil {
				return err
			}
			if imp.Resolved != "" && imp.Resolved != target.Abs {
				return diagnostic.New(diagnostic.Source, "graph.resolution-mismatch",
					"tsgo 把 %q 解析为 %s，项目解析为 %s", imp.Specifier, imp.Resolved, target.Abs)
			}
			g.Edges = append(g.Edges, Edge{
				From:      file.Path,
				To:        target.Path,
				Specifier: imp.Specifier,
				TypeOnly:  imp.TypeOnly,
				Start:     imp.Start,
				Length:    imp.Length,
			})
			if err := visit(target); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[file.Path] = 2
		return nil
	}
	if err := visit(entry); err != nil {
		return nil, err
	}
	g.Hash = hashFiles(g.Files)
	return g, nil
}

func cycleText(stack []string, repeated string) string {
	start := 0
	for i, path := range stack {
		if path == repeated {
			start = i
			break
		}
	}
	parts := append([]string{}, stack[start:]...)
	parts = append(parts, repeated)
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += " -> "
		}
		out += part
	}
	return out
}

func hashFiles(files []project.File) string {
	lines := make([]string, 0, len(files))
	for _, file := range files {
		lines = append(lines, file.Path+"\x00"+file.SHA256)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Snapshot 用图里已经固定的字节建立快照，不再读磁盘。
func (g *Graph) Snapshot(file project.File, limits source.Limits) (*source.Snapshot, error) {
	if g == nil {
		return nil, diagnostic.New(diagnostic.Internal, "graph.nil", "依赖图为空")
	}
	return file.Snapshot(limits)
}

// Report 返回不含源字节的报告。
func (g *Graph) Report() Report {
	report := Report{Entry: g.Entry.Path, Hash: g.Hash}
	for _, file := range g.Files {
		report.Files = append(report.Files, FileInfo{Path: file.Path, SHA256: file.SHA256})
	}
	report.Edges = append([]Edge{}, g.Edges...)
	return report
}

func (g *Graph) String() string {
	return fmt.Sprintf("graph(%s, %d files, %d edges)", g.Entry.Path, len(g.Files), len(g.Edges))
}
