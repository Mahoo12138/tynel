// Package project 把一份 TypeScript 项目固定在某个根目录下，并把入口和显式
// 相对导入解析成文件身份。
//
// 解析只做三件事：确认路径在许可根内、按大小写精确地找到文件、拒绝任何需要
// 猜测的写法。它不读 tsconfig、不解析裸模块、不尝试补 .ts 后缀，也不在失败
// 后到别处搜索。文件身份由项目内相对路径和内容哈希组成，所以同一个根从不同
// 工作目录打开时，解析结果一致。
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// Root 是一个已经规范化的项目根。它只接受目录。
type Root struct {
	path string
}

// File 是解析结果。Path 是项目内相对路径（斜杠分隔），Abs 是规范化绝对路径，
// SHA256 是解析时读到的内容哈希。Bytes 只在本次解析内有效，调用方不应修改。
type File struct {
	Path   string
	Abs    string
	SHA256 string
	Bytes  []byte
}

// Identity 返回稳定的文件身份：相对路径加内容哈希。
func (f File) Identity() string { return f.Path + "@" + f.SHA256 }

// Snapshot 用解析时读到的字节建立源码快照。快照的 Path 使用绝对路径，
// 诊断因此不依赖当前工作目录。
func (f File) Snapshot(limits source.Limits) (*source.Snapshot, error) {
	return source.New(f.Abs, f.Bytes, limits)
}

// Open 把 dir 规范化为项目根。相对路径按当前工作目录解释，随后解析符号链接，
// 保证同一个目录从不同工作目录打开得到同一个根。
func Open(dir string) (*Root, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, diagnostic.New(diagnostic.Usage, "project.no-root", "项目根不能为空")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-invalid", "无法解析项目根 %s", dir).WithDetail(err.Error())
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-missing", "项目根 %s 不存在或不可访问", dir).WithDetail(err.Error())
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-missing", "项目根 %s 不可访问", dir).WithDetail(err.Error())
	}
	if !info.IsDir() {
		return nil, diagnostic.New(diagnostic.Environment, "project.root-not-dir", "项目根 %s 不是目录", dir)
	}
	return &Root{path: filepath.Clean(real)}, nil
}

// Path 返回规范化的绝对根路径。
func (r *Root) Path() string { return r.path }

// ResolveEntry 解析入口。name 可以是项目内相对路径，也可以是许可根内的绝对
// 路径；它必须以 .ts 结尾，不补后缀，也不接受裸模块名。
func (r *Root) ResolveEntry(name string) (File, error) {
	if strings.TrimSpace(name) == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.missing-entry", "缺少入口文件")
	}
	if filepath.IsAbs(name) {
		clean := filepath.Clean(name)
		if real, err := filepath.EvalSymlinks(clean); err == nil {
			clean = real
		}
		rel, err := filepath.Rel(r.path, clean)
		if err != nil {
			return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "入口 %s 不在项目根 %s 内", name, r.path)
		}
		name = filepath.ToSlash(rel)
	}
	return r.resolve(name, "入口")
}

// ResolveImport 从 from 所在目录解析一条显式相对导入。request 必须以 ./ 或
// ../ 开头，并以 .ts 结尾；裸模块名、绝对路径和目录都不接受。
func (r *Root) ResolveImport(from File, request string) (File, error) {
	if from.Path == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.no-importer", "导入缺少来源文件")
	}
	if !within(r.path, from.Abs) {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "来源文件 %s 不在项目根 %s 内", from.Path, r.path)
	}
	if strings.TrimSpace(request) == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.bad-request", "导入路径为空")
	}
	if filepath.IsAbs(request) || strings.HasPrefix(request, "/") {
		return File{}, diagnostic.New(diagnostic.Source, "project.absolute-path", "导入 %q 是绝对路径；只接受 ./ 或 ../ 开头的项目内路径", request)
	}
	if !strings.HasPrefix(request, "./") && !strings.HasPrefix(request, "../") {
		return File{}, diagnostic.New(diagnostic.Source, "project.bare-module", "导入 %q 是裸模块名；本阶段只接受显式相对 .ts 路径", request)
	}
	joined := path.Join(path.Dir(from.Path), request)
	return r.resolve(joined, "导入")
}

// resolve 把项目内相对路径解析成文件。它先按字面路径逐段精确查找，再处理
// 符号链接；任何一步失败都返回明确错误，不做后备搜索。
func (r *Root) resolve(rel, what string) (File, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return File{}, diagnostic.New(diagnostic.Source, "project.bad-request", "%s路径为空", what)
	}
	if strings.Contains(rel, "\\") {
		return File{}, diagnostic.New(diagnostic.Source, "project.bad-request", "%s路径 %q 含反斜杠；只接受斜杠分隔", what, rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %q 越出项目根 %s", what, rel, r.path)
	}
	if !strings.HasSuffix(clean, ".ts") {
		return File{}, diagnostic.New(diagnostic.Source, "project.non-ts", "%s %q 不是 .ts 文件", what, rel)
	}

	abs, err := r.exactPath(clean, what)
	if err != nil {
		return File{}, err
	}

	info, err := os.Lstat(abs)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法读取%s %s", what, clean).WithDetail(err.Error())
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return File{}, diagnostic.New(diagnostic.Source, "project.missing", "%s %s 的符号链接目标不存在", what, clean)
		}
		if !within(r.path, target) {
			return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 的符号链接指向项目根外：%s", what, clean, target)
		}
		abs = target
		info, err = os.Stat(abs)
		if err != nil {
			return File{}, diagnostic.New(diagnostic.Source, "project.missing", "%s %s 的符号链接目标不可读", what, clean)
		}
	}
	if !info.Mode().IsRegular() {
		return File{}, diagnostic.New(diagnostic.Source, "project.not-file", "%s %s 不是普通文件", what, clean)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return File{}, diagnostic.New(diagnostic.Environment, "project.io", "无法读取%s %s", what, clean).WithDetail(err.Error())
	}
	relToRoot, err := filepath.Rel(r.path, abs)
	if err != nil || !within(r.path, abs) {
		return File{}, diagnostic.New(diagnostic.Source, "project.outside-root", "%s %s 不在项目根 %s 内", what, clean, r.path)
	}
	sum := sha256.Sum256(data)
	return File{
		Path:   filepath.ToSlash(relToRoot),
		Abs:    abs,
		SHA256: hex.EncodeToString(sum[:]),
		Bytes:  data,
	}, nil
}

// exactPath 逐段检查目录项，要求大小写完全匹配。它同时识别两种情况：
// 请求的大小写与磁盘不一致，以及同一目录里存在仅大小写不同的多个条目。
func (r *Root) exactPath(rel, what string) (string, error) {
	cur := r.path
	for _, part := range strings.Split(rel, "/") {
		entries, err := os.ReadDir(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return "", diagnostic.New(diagnostic.Source, "project.missing", "%s %s 不存在", what, rel)
			}
			return "", diagnostic.New(diagnostic.Environment, "project.io", "无法读取目录 %s", cur).WithDetail(err.Error())
		}
		var exact string
		var folded []string
		for _, entry := range entries {
			if entry.Name() == part {
				exact = entry.Name()
			}
			if strings.EqualFold(entry.Name(), part) {
				folded = append(folded, entry.Name())
			}
		}
		if exact == "" {
			if len(folded) > 0 {
				return "", diagnostic.New(diagnostic.Source, "project.case-conflict", "%s %s 的大小写与磁盘不一致：磁盘上是 %s", what, rel, strings.Join(folded, "、"))
			}
			return "", diagnostic.New(diagnostic.Source, "project.missing", "%s %s 不存在", what, rel)
		}
		if len(folded) > 1 {
			return "", diagnostic.New(diagnostic.Source, "project.case-conflict", "%s %s 存在仅大小写不同的条目：%s", what, rel, strings.Join(folded, "、"))
		}
		cur = filepath.Join(cur, exact)
	}
	return cur, nil
}

// within 报告 target 是否在 root 内（含 root 本身）。两侧都应是规范化绝对路径。
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (f File) String() string {
	short := f.SHA256
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("%s (%s)", f.Path, short)
}
