package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/nir"
)

// TestBuildIRRejectsBadNIR 把坏 NIR 直接交给主命令行的 build-ir，确认
// 协议、作用域、预算和结构错误不会绕过验证器。
func TestBuildIRRejectsBadNIR(t *testing.T) {
	root, _ := newFakeProject(t)
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "旧协议",
			body: `{"schemaVersion":2,"entry":"main","functions":[]}`,
			code: "nir.version",
		},
		{
			name: "未知操作",
			body: `{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":null,"result":"void","body":[{"op":"wat"}]}]}`,
			code: "nir.bad-op",
		},
		{
			name: "未定义局部值",
			body: `{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":null,"result":"void","body":[{"op":"print","value":{"op":"local","name":"x"}},{"op":"return"}]}]}`,
			code: "nir.unknown-local",
		},
		{
			name: "重复定义",
			body: `{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":null,"result":"void","body":[{"op":"let","name":"x","value":{"op":"number","bits":"0x3ff0000000000000"}},{"op":"let","name":"x","value":{"op":"number","bits":"0x4000000000000000"}},{"op":"return"}]}]}`,
			code: "nir.duplicate-name",
		},
		{
			name: "短路缺少右值",
			body: `{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":null,"result":"void","body":[{"op":"short","name":"t","operator":"&&","left":{"op":"bool","value":true}},{"op":"return"}]}]}`,
			code: "nir.missing",
		},
		{
			name: "三元式两臂类型不同",
			body: `{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":null,"result":"void","body":[{"op":"select","name":"t","cond":{"op":"bool","value":true},"thenValue":{"op":"number","bits":"0x3ff0000000000000"},"elseValue":{"op":"bool","value":false},"type":"number"},{"op":"return"}]}]}`,
			code: "nir.result-type",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.nir.json")
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatalf("写入 NIR 失败：%v", err)
			}
			a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: fakeRunner{}, root: root, hasRoot: true}
			err := a.buildIR([]string{path, "-o", filepath.Join(t.TempDir(), "out")})
			if err == nil {
				t.Fatal("坏 NIR 应被拒绝")
			}
			var d *diagnostic.Diagnostic
			if !errors.As(err, &d) || d.Code != c.code {
				t.Fatalf("错误码应为 %s，实际 %v", c.code, err)
			}
		})
	}
}

// TestBuildIRRejectsDeepNesting 生成超过深度预算的 if 链，确认 build-ir 在
// 读取阶段就停住，不会把嵌套留给后端。
func TestBuildIRRejectsDeepNesting(t *testing.T) {
	root, _ := newFakeProject(t)
	trueValue := true
	body := []nir.Stmt{{Op: nir.OpReturn}}
	for i := 0; i <= nir.DefaultLimits().MaxDepth; i++ {
		cond := nir.Expr{Op: nir.OpBool, Value: &trueValue}
		body = []nir.Stmt{{Op: nir.OpIf, Cond: &cond, Then: body}}
	}
	module := nir.Module{
		SchemaVersion: nir.SchemaVersion,
		Entry:         "main",
		Functions:     []nir.Function{{Name: "main", Result: "void", Body: body}},
	}
	data, err := json.Marshal(module)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	path := filepath.Join(t.TempDir(), "deep.nir.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入 NIR 失败：%v", err)
	}
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: fakeRunner{}, root: root, hasRoot: true}
	err = a.buildIR([]string{path, "-o", filepath.Join(t.TempDir(), "out")})
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "nir.budget" {
		t.Fatalf("错误码应为 nir.budget，实际 %v", err)
	}
}
