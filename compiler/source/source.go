// Package source 把一份 TypeScript 源文件固定成不可变的字节快照，并在快照上
// 做位置换算。
//
// 编译器的所有位置都来自快照，不来自磁盘：文件只在 Read 时读一次，之后即使
// 原文件被改写，快照内容与位置也不会变。上游前端给出的位置是 UTF-8 字节偏移，
// 本包负责把字节偏移换算成行列，并拒绝落在字符中间或越界的偏移。
//
// 换算不改变字节：Range 始终是快照里的字节区间，Slice 直接切这段字节。行号与
// 列号只用于展示，列按字符（Unicode 码点）计数，不是字节数也不是 UTF-16 码元。
package source

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"sync/atomic"
	"unicode/utf8"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// FileID 标识一份源文件。0 表示无效，其余值在一个进程内唯一。
//
// ID 只用来区分文件，不跨进程、不写进产物：它由计数器分配，换一次运行就会变。
// 需要稳定身份的地方用 Path 或内容哈希，不要用 ID。
type FileID uint32

var lastFileID atomic.Uint32

// Limits 是读入预算。超限的文件按源码错误拒绝，不做截断——截断后继续编译会
// 得到指向错误位置的行列，比直接拒绝更难排查。
type Limits struct {
	MaxBytes int
	MaxLines int
}

// DefaultLimits 是当前采用的预算：1 MiB、10 万行。
func DefaultLimits() Limits {
	return Limits{MaxBytes: 1 << 20, MaxLines: 100_000}
}

// Position 是 1 起始的行列。Column 按字符计数，制表符算一个字符。
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Range 是快照内的字节区间，左闭右开。
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Snapshot 是一份源文件的不可变副本。
type Snapshot struct {
	id    FileID
	path  string
	data  []byte
	lines []int // 每一行的起始字节偏移，升序
	bom   bool
}

// Read 读取一次磁盘上的文件并固定成快照。这是本包唯一访问文件系统的地方。
func Read(path string, limits Limits) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "source.io", "无法读取源文件 %s", path).WithDetail(err.Error())
	}
	return New(path, data, limits)
}

// New 用已有字节建立快照。
//
// 字节会被复制一份：调用方之后改动自己的切片，不会影响快照，也不会影响已经
// 算出的位置。UTF-8 BOM 在复制时去掉，因为前端拿到的是去掉 BOM 的内容，位置
// 必须与它一致。
func New(path string, data []byte, limits Limits) (*Snapshot, error) {
	if path == "" {
		return nil, diagnostic.New(diagnostic.Internal, "source.no-path", "源文件缺少路径")
	}
	if len(data) > limits.MaxBytes {
		return nil, diagnostic.New(diagnostic.Source, "source.too-large", "源文件 %s 有 %d 字节，超过预算 %d 字节", path, len(data), limits.MaxBytes)
	}

	owned := bytes.Clone(data)
	bom := false
	if bytes.HasPrefix(owned, []byte{0xEF, 0xBB, 0xBF}) {
		owned = owned[3:]
		bom = true
	}
	if offset, ok := firstInvalidUTF8(owned); !ok {
		return nil, diagnostic.New(diagnostic.Source, "source.encoding", "源文件 %s 在字节 %d 处不是合法的 UTF-8", path, offset)
	}

	lines := []int{0}
	for i, b := range owned {
		if b == '\n' {
			lines = append(lines, i+1)
		}
	}
	if len(lines) > limits.MaxLines {
		return nil, diagnostic.New(diagnostic.Source, "source.too-many-lines", "源文件 %s 有 %d 行，超过预算 %d 行", path, len(lines), limits.MaxLines)
	}

	return &Snapshot{
		id:    FileID(lastFileID.Add(1)),
		path:  path,
		data:  owned,
		lines: lines,
		bom:   bom,
	}, nil
}

// firstInvalidUTF8 返回第一个不合法字节的位置；内容合法时返回 ok=true。
func firstInvalidUTF8(data []byte) (offset int, ok bool) {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return i, false
		}
		i += size
	}
	return 0, true
}

// ID 返回文件标识。
func (s *Snapshot) ID() FileID { return s.id }

// Path 返回建立快照时使用的路径。
func (s *Snapshot) Path() string { return s.path }

// Bytes 返回快照字节。调用方不应修改返回值；需要改动请建立新快照。
func (s *Snapshot) Bytes() []byte { return s.data }

// Text 返回快照内容。
func (s *Snapshot) Text() string { return string(s.data) }

// Size 返回字节数。
func (s *Snapshot) Size() int { return len(s.data) }

// LineCount 返回行数。以换行结尾的文件会多出一个空行。
func (s *Snapshot) LineCount() int { return len(s.lines) }

// HadBOM 报告原文件是否带 UTF-8 BOM。
func (s *Snapshot) HadBOM() bool { return s.bom }

// Position 把字节偏移换算成行列。
//
// 偏移必须落在 [0, Size] 内，且不能落在多字节字符中间。越界或落在字符中间时
// 返回内部故障：偏移来自前端或编译器自己，出错说明我们算错了，不是用户输入
// 的问题，所以不能当成源码错误。
func (s *Snapshot) Position(offset int) (Position, error) {
	if offset < 0 || offset > len(s.data) {
		return Position{}, badOffset(s, offset, "超出快照范围 0..%d", len(s.data))
	}
	if offset < len(s.data) && !utf8.RuneStart(s.data[offset]) {
		return Position{}, badOffset(s, offset, "落在多字节字符中间")
	}
	line := sort.Search(len(s.lines), func(i int) bool { return s.lines[i] > offset }) - 1
	if line < 0 {
		line = 0
	}
	return Position{
		Line:   line + 1,
		Column: utf8.RuneCount(s.data[s.lines[line]:offset]) + 1,
	}, nil
}

// Range 校验并返回一段字节区间。
func (s *Snapshot) Range(start, end int) (Range, error) {
	if start < 0 || end < start || end > len(s.data) {
		return Range{}, badOffset(s, start, "区间 [%d, %d) 不合法", start, end)
	}
	if _, err := s.Position(start); err != nil {
		return Range{}, err
	}
	if _, err := s.Position(end); err != nil {
		return Range{}, err
	}
	return Range{Start: start, End: end}, nil
}

// Slice 返回区间对应的文本。区间不合法时返回错误，不会返回半段字符。
func (s *Snapshot) Slice(r Range) (string, error) {
	if _, err := s.Range(r.Start, r.End); err != nil {
		return "", err
	}
	return string(s.data[r.Start:r.End]), nil
}

// LineText 返回第 line 行（1 起始）的文本，不含行尾换行符。
func (s *Snapshot) LineText(line int) (string, error) {
	if line < 1 || line > len(s.lines) {
		d := diagnostic.New(diagnostic.Internal, "source.bad-line", "源文件 %s 没有第 %d 行", s.path, line)
		return "", d.WithDetail(fmt.Sprintf("行号范围 1..%d", len(s.lines)))
	}
	start := s.lines[line-1]
	end := len(s.data)
	if line < len(s.lines) {
		end = s.lines[line] - 1 // 去掉 '\n'
	}
	text := s.data[start:end]
	return string(bytes.TrimSuffix(text, []byte{'\r'})), nil
}

func badOffset(s *Snapshot, offset int, format string, args ...any) error {
	d := diagnostic.New(diagnostic.Internal, "source.bad-offset", "源文件 %s 的偏移 %d 不可用", s.path, offset)
	return d.WithDetail(fmt.Sprintf(format, args...))
}
