package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
)

func sample() *Result {
	return &Result{
		SchemaVersion: SchemaVersion,
		File:          "/tmp/add.ts",
		SourceSHA256:  strings.Repeat("a", 64),
		SourceBytes:   209,
		Diagnostics: []Diagnostic{
			{File: "/tmp/add.ts", Line: 2, Column: 7, Severity: "error", Code: 2322, Message: "类型不符"},
		},
		Capability: []Finding{
			{File: "/tmp/add.ts", Line: 3, Column: 1, Category: "能力限制", Code: "cap.any", Message: "不支持 any"},
		},
		Summary: Summary{Functions: 2, Verified: true},
		Facts:   &frontend.Facts{SchemaVersion: 1, Session: "s", File: "/tmp/add.ts"},
		NIR: &nir.Module{
			SchemaVersion: nir.SchemaVersion,
			Entry:         "main",
			Functions:     []nir.Function{{Name: "main", Result: nir.TypeVoid, Body: []nir.Stmt{{Op: nir.OpReturn}}}},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, sample()); err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	got, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if got.File != "/tmp/add.ts" || got.SourceBytes != 209 || got.SourceSHA256 != strings.Repeat("a", 64) {
		t.Errorf("源码身份丢失：%+v", got)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != 2322 {
		t.Errorf("诊断丢失：%+v", got.Diagnostics)
	}
	if len(got.Capability) != 1 || got.Capability[0].Code != "cap.any" {
		t.Errorf("能力项丢失：%+v", got.Capability)
	}
	if got.Facts == nil || got.Facts.Session != "s" {
		t.Errorf("事实丢失：%+v", got.Facts)
	}
	if got.NIR == nil || got.NIR.Entry != "main" {
		t.Errorf("NIR 丢失：%+v", got.NIR)
	}
	if !got.Summary.Verified || got.Summary.Functions != 2 {
		t.Errorf("摘要丢失：%+v", got.Summary)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	_, err := Decode([]byte(`{"schemaVersion":1,"surprise":true}`))
	if err == nil || !strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("未知字段应被拒绝，实际 %v", err)
	}
}

func TestDecodeRejectsTrailingContent(t *testing.T) {
	_, err := Decode([]byte(`{"schemaVersion":1}{"schemaVersion":1}`))
	if err == nil || !strings.Contains(err.Error(), "之后还有内容") {
		t.Fatalf("尾随内容应被拒绝，实际 %v", err)
	}
}

func TestDecodeRejectsVersion(t *testing.T) {
	_, err := Decode([]byte(`{"schemaVersion":2}`))
	if err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("版本不符应被拒绝，实际 %v", err)
	}
}

func TestDecodeFillsEmptyLists(t *testing.T) {
	got, err := Decode([]byte(`{"schemaVersion":1,"file":"/tmp/a.ts","diagnostics":null,"capability":null}`))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if got.Diagnostics == nil || got.Capability == nil {
		t.Fatalf("空列表应被填成空切片而不是 nil：%+v", got)
	}
}

func TestExitCodeMapping(t *testing.T) {
	clean := &Result{}
	if code := clean.ExitCode(); code != 0 {
		t.Errorf("干净结果应为 0，实际 %d", code)
	}
	withError := &Result{Diagnostics: []Diagnostic{{Severity: "error"}}}
	if code := withError.ExitCode(); code != 1 {
		t.Errorf("源码错误应为 1，实际 %d", code)
	}
	withWarning := &Result{Diagnostics: []Diagnostic{{Severity: "warning"}}}
	if code := withWarning.ExitCode(); code != 0 {
		t.Errorf("警告不应影响退出状态，实际 %d", code)
	}
	withCapability := &Result{Capability: []Finding{{Code: "cap.any"}}}
	if code := withCapability.ExitCode(); code != 3 {
		t.Errorf("能力限制应为 3，实际 %d", code)
	}
}
