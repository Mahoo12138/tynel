package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/tyneladapter"
)

// open 是测试里的统一入口：把源码写进一个临时文件再读回来，模拟宿主
// 「读一次字节快照」的行为。
func open(t *testing.T, name, source string) (*tyneladapter.Session, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("写入夹具失败：%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取夹具失败：%v", err)
	}
	session, err := tyneladapter.Open(tyneladapter.Options{FileName: path, Source: data})
	if err != nil {
		t.Fatalf("建立会话失败：%v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, path
}

func TestCleanProgramHasNoDiagnostics(t *testing.T) {
	session, _ := open(t, "add.ts", `function add(a: number, b: number): number {
  return a + b;
}
const answer: number = add(40, 2);
`)

	diagnostics, err := session.Diagnostics()
	if err != nil {
		t.Fatalf("读取诊断失败：%v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("干净的源文件不应有诊断，实际有 %d 条：%+v", len(diagnostics), diagnostics)
	}
}

func TestTypeErrorCarriesCodeAndPosition(t *testing.T) {
	session, path := open(t, "bad.ts", `const answer: number = "forty-two";
`)

	diagnostics, err := session.Diagnostics()
	if err != nil {
		t.Fatalf("读取诊断失败：%v", err)
	}
	if len(diagnostics) == 0 {
		t.Fatal("类型错误应产生诊断")
	}
	first := diagnostics[0]
	if first.Category != "error" {
		t.Errorf("类别应为 error，实际为 %q", first.Category)
	}
	if first.Code != 2322 {
		t.Errorf("错误码应为 2322（类型不可赋值），实际为 %d：%s", first.Code, first.Message)
	}
	if !strings.Contains(first.Message, "not assignable") {
		t.Errorf("消息应说明不可赋值：%q", first.Message)
	}
	if first.File != path {
		t.Errorf("诊断应指向 %s，实际为 %s", path, first.File)
	}
	if first.Start < 0 || first.Start > len(`const answer: number = "forty-two";`) {
		t.Errorf("起始偏移超出源码范围：%d", first.Start)
	}
	if first.Length <= 0 {
		t.Errorf("长度应为正数：%d", first.Length)
	}
}

func TestPositionIsUTF8ByteOffset(t *testing.T) {
	// 上游按字节计位置。用一行中文注释把同一个字符位置推开 102 个字节，
	// 诊断偏移应当落在字节位置上，而不是字符位置或 UTF-16 位置上。
	const comment = "// 一个类型错误：数字变量被赋了字符串。用来确认诊断带着错误码和位置。\n"
	session, _ := open(t, "bad.ts", comment+`const answer: number = "forty-two";`+"\n")

	diagnostics, err := session.Diagnostics()
	if err != nil {
		t.Fatalf("读取诊断失败：%v", err)
	}
	if len(diagnostics) == 0 {
		t.Fatal("类型错误应产生诊断")
	}
	first := diagnostics[0]
	want := len([]byte(comment)) + len([]byte("const "))
	if first.Start != want {
		t.Fatalf("偏移应为 UTF-8 字节位置 %d，实际为 %d", want, first.Start)
	}
	if charPos := len([]rune(comment)) + len("const "); first.Start == charPos {
		t.Fatalf("偏移看起来是字符位置 %d，说明单位判断错了", charPos)
	}
}

func TestDiagnosticsArePlainData(t *testing.T) {
	// 诊断字段全是普通类型：会话关闭后仍然可以读取，说明没有夹带上游句柄。
	session, _ := open(t, "bad.ts", "const x: number = true;\n")
	diagnostics, err := session.Diagnostics()
	if err != nil {
		t.Fatalf("读取诊断失败：%v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("关闭会话失败：%v", err)
	}
	for _, d := range diagnostics {
		if d.Message == "" || d.File == "" {
			t.Errorf("诊断内容不完整：%+v", d)
		}
	}
}

func TestCloseIsIdempotentAndQueriesFailAfterwards(t *testing.T) {
	session, _ := open(t, "add.ts", "const answer = 42;\n")

	if err := session.Close(); err != nil {
		t.Fatalf("第一次关闭应成功：%v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("重复关闭应返回 nil：%v", err)
	}
	if _, err := session.Diagnostics(); !errors.Is(err, tyneladapter.ErrSessionClosed) {
		t.Fatalf("关闭后查询应返回 ErrSessionClosed，实际为 %v", err)
	}
}

func TestRejectsInvalidOptions(t *testing.T) {
	cases := []struct {
		name string
		opts tyneladapter.Options
	}{
		{"空文件名", tyneladapter.Options{Source: []byte("const x = 1;")}},
		{"空源码", tyneladapter.Options{FileName: "/tmp/x.ts"}},
		{"相对路径", tyneladapter.Options{FileName: "x.ts", Source: []byte("const x = 1;")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := tyneladapter.Open(c.opts); err == nil {
				t.Fatal("非法参数应返回错误")
			}
		})
	}
}

func TestSourceSnapshotIsNotRereadFromDisk(t *testing.T) {
	// 会话只使用调用方给的字节。检查期间改写磁盘上的文件，诊断不应变化。
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.ts")
	if err := os.WriteFile(path, []byte("const x: number = 1;\n"), 0o644); err != nil {
		t.Fatalf("写入夹具失败：%v", err)
	}
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取夹具失败：%v", err)
	}
	if err := os.WriteFile(path, []byte("const x: number = \"改坏了\";\n"), 0o644); err != nil {
		t.Fatalf("改写夹具失败：%v", err)
	}

	session, err := tyneladapter.Open(tyneladapter.Options{FileName: path, Source: snapshot})
	if err != nil {
		t.Fatalf("建立会话失败：%v", err)
	}
	defer session.Close()
	diagnostics, err := session.Diagnostics()
	if err != nil {
		t.Fatalf("读取诊断失败：%v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("快照内容合法，不应读到磁盘上的改动：%+v", diagnostics)
	}
}
