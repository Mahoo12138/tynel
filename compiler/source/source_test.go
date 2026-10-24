package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// fixture 用默认预算建立快照，失败即终止用例。
func fixture(t *testing.T, text string) *Snapshot {
	t.Helper()
	s, err := New("/tmp/fixture.ts", []byte(text), DefaultLimits())
	if err != nil {
		t.Fatalf("建立快照失败：%v", err)
	}
	return s
}

func TestPositionAfterChinesePrefix(t *testing.T) {
	// 第一行是 7 个字符、15 个字节的中文注释；第二行的 answer 在字节 22、
	// 字符 7 的位置。列按字符算，所以中文前缀不会把列推远。
	text := "// 中文注释\nconst answer: number = 1;\n"
	s := fixture(t, text)
	offset := strings.Index(text, "answer")
	if offset != 22 {
		t.Fatalf("夹具的字节偏移应为 22，实际为 %d", offset)
	}

	pos, err := s.Position(offset)
	if err != nil {
		t.Fatalf("换算位置失败：%v", err)
	}
	if pos.Line != 2 || pos.Column != 7 {
		t.Fatalf("期望 2:7，实际 %d:%d", pos.Line, pos.Column)
	}
}

func TestPositionCountsRunesNotBytes(t *testing.T) {
	// emoji 占 4 个字节、1 个字符。x 的字节偏移是 24，字符列是 22。
	text := `const s = "😀"; const x = 1;` + "\n"
	s := fixture(t, text)
	offset := strings.Index(text, "const x")
	if offset != 18 {
		t.Fatalf("第二个 const 的字节偏移应为 18，实际为 %d", offset)
	}
	pos, err := s.Position(offset + len("const "))
	if err != nil {
		t.Fatalf("换算位置失败：%v", err)
	}
	if pos.Line != 1 || pos.Column != 22 {
		t.Fatalf("期望 1:22，实际 %d:%d", pos.Line, pos.Column)
	}
}

func TestPositionIsStableAcrossQueries(t *testing.T) {
	text := "// 前缀\nfunction add(a: number) { return a; }\n"
	s := fixture(t, text)
	offset := strings.Index(text, "add(a")
	first, err := s.Position(offset)
	if err != nil {
		t.Fatalf("换算位置失败：%v", err)
	}
	for i := 0; i < 200; i++ {
		again, err := s.Position(offset)
		if err != nil {
			t.Fatalf("第 %d 次查询失败：%v", i, err)
		}
		if again != first {
			t.Fatalf("第 %d 次查询得到 %v，首次为 %v", i, again, first)
		}
	}
}

func TestPositionRejectsInvalidOffsets(t *testing.T) {
	text := "// 中文\nconst x = 1;\n"
	s := fixture(t, text)
	middle := strings.Index(text, "中") + 1 // 多字节字符的第二个字节

	cases := []struct {
		name   string
		offset int
	}{
		{"负数", -1},
		{"越过末尾", s.Size() + 1},
		{"字符中间", middle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.Position(c.offset); err == nil {
				t.Fatalf("偏移 %d 应被拒绝", c.offset)
			} else if !isCode(err, "source.bad-offset") {
				t.Fatalf("错误码应为 source.bad-offset，实际 %v", err)
			}
		})
	}

	// 末尾偏移是合法的：它表示文件结束。
	if _, err := s.Position(s.Size()); err != nil {
		t.Fatalf("末尾偏移应可用：%v", err)
	}
}

func TestBudgetRejectsLargeInput(t *testing.T) {
	limits := Limits{MaxBytes: 16, MaxLines: 4}
	if _, err := New("/tmp/big.ts", []byte(strings.Repeat("a", 17)), limits); !isCode(err, "source.too-large") {
		t.Fatalf("超限字节应报 source.too-large，实际 %v", err)
	}
	lines := "a\nb\nc\nd\ne\n"
	if _, err := New("/tmp/lines.ts", []byte(lines), limits); !isCode(err, "source.too-many-lines") {
		t.Fatalf("超限行数应报 source.too-many-lines，实际 %v", err)
	}
}

func TestBadEncodingIsSourceError(t *testing.T) {
	data := []byte("const x = 1;\n// \xff\xfe\n")
	_, err := New("/tmp/bad.ts", data, DefaultLimits())
	if !isCode(err, "source.encoding") {
		t.Fatalf("坏编码应报 source.encoding，实际 %v", err)
	}
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Category != diagnostic.Source {
		t.Fatalf("坏编码属于源码错误，实际类别 %v", d)
	}
	if !strings.Contains(d.Message, "字节 16") {
		t.Fatalf("消息应指出出错字节，实际 %q", d.Message)
	}
}

func TestBOMIsStripped(t *testing.T) {
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte("const x = 1;\n")...)
	s, err := New("/tmp/bom.ts", data, DefaultLimits())
	if err != nil {
		t.Fatalf("建立快照失败：%v", err)
	}
	if !s.HadBOM() {
		t.Fatal("应记录文件带 BOM")
	}
	if strings.HasPrefix(s.Text(), "\ufeff") {
		t.Fatal("快照内容不应包含 BOM")
	}
	pos, err := s.Position(0)
	if err != nil {
		t.Fatalf("换算位置失败：%v", err)
	}
	if pos.Line != 1 || pos.Column != 1 {
		t.Fatalf("BOM 之后的第一个字符应为 1:1，实际 %d:%d", pos.Line, pos.Column)
	}
}

func TestReadSeparatesIOErrors(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "missing.ts"), DefaultLimits())
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) {
		t.Fatalf("读取失败应返回诊断，实际 %v", err)
	}
	if d.Category != diagnostic.Environment || d.Code != "source.io" {
		t.Fatalf("I/O 失败应为环境缺失，实际 %v / %s", d.Category, d.Code)
	}
}

func TestSnapshotDoesNotFollowLaterDiskChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "add.ts")
	if err := os.WriteFile(path, []byte("const answer = 42;\n"), 0o644); err != nil {
		t.Fatalf("写入夹具失败：%v", err)
	}
	s, err := Read(path, DefaultLimits())
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if err := os.WriteFile(path, []byte("const answer = \"改坏了\";\n"), 0o644); err != nil {
		t.Fatalf("改写夹具失败：%v", err)
	}
	if s.Text() != "const answer = 42;\n" {
		t.Fatalf("快照内容不应随磁盘变化：%q", s.Text())
	}
}

func TestSnapshotOwnsItsBytes(t *testing.T) {
	data := []byte("const x = 1;\n")
	s, err := New("/tmp/own.ts", data, DefaultLimits())
	if err != nil {
		t.Fatalf("建立快照失败：%v", err)
	}
	data[0] = 'X'
	if s.Text() != "const x = 1;\n" {
		t.Fatalf("调用方改动自己的切片不应影响快照：%q", s.Text())
	}
}

func TestLineTextAndCRLF(t *testing.T) {
	s := fixture(t, "first\r\nsecond\r\n")
	first, err := s.LineText(1)
	if err != nil {
		t.Fatalf("读取第一行失败：%v", err)
	}
	if first != "first" {
		t.Fatalf("第一行应为 first，实际 %q", first)
	}
	if s.LineCount() != 3 {
		t.Fatalf("以换行结尾的文件应有 3 行（含末尾空行），实际 %d", s.LineCount())
	}
	pos, err := s.Position(len("first\r\n"))
	if err != nil {
		t.Fatalf("换算位置失败：%v", err)
	}
	if pos.Line != 2 || pos.Column != 1 {
		t.Fatalf("第二行开头应为 2:1，实际 %d:%d", pos.Line, pos.Column)
	}
	if _, err := s.LineText(9); !isCode(err, "source.bad-line") {
		t.Fatalf("越界行号应报 source.bad-line，实际 %v", err)
	}
}

func TestSliceRejectsPartialRune(t *testing.T) {
	s := fixture(t, "const 名 = 1;\n")
	start := strings.Index(s.Text(), "名")
	if _, err := s.Slice(Range{Start: start, End: start + 1}); !isCode(err, "source.bad-offset") {
		t.Fatalf("切在多字节字符中间应被拒绝，实际 %v", err)
	}
	text, err := s.Slice(Range{Start: start, End: start + 3})
	if err != nil {
		t.Fatalf("完整字符应可切片：%v", err)
	}
	if text != "名" {
		t.Fatalf("切片结果应为 名，实际 %q", text)
	}
}

func isCode(err error, code string) bool {
	var d *diagnostic.Diagnostic
	return errors.As(err, &d) && d.Code == code
}
