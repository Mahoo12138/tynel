package main

import (
	"path/filepath"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/capability"
	"github.com/Mahoo12138/tynel/compiler/source"
)

// capabilityCodes 走宿主的真实顺序：先读快照、再做源码检查，最后做能力检查。
func capabilityCodes(t *testing.T, name string) []string {
	t.Helper()
	path := fixturePath(t, filepath.Join("capability", name))
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("源码检查失败：%v", err)
	}
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			t.Fatalf("夹具 %s 应先通过类型检查，实际有诊断：%s", name, d)
		}
	}
	facts, err := factsOf(snapshot)
	if err != nil {
		t.Fatalf("读取事实失败：%v", err)
	}
	findings, err := capability.Check(snapshot, facts)
	if err != nil {
		t.Fatalf("能力检查失败：%v", err)
	}
	codes := make([]string, 0, len(findings))
	for _, d := range findings {
		codes = append(codes, d.Code)
	}
	return codes
}

func TestCapabilityFixtures(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"ok.ts", ""},
		{"any.ts", "cap.any"},
		{"assert.ts", "cap.type-escape"},
		{"object.ts", "cap.unsupported-syntax"},
		{"eval.ts", "cap.forbidden-global"},
		{"dynamic-function.ts", "cap.unsupported-syntax"},
		{"library.ts", "cap.unknown-callee"},
		{"uncalled.ts", "cap.unsupported-syntax"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			codes := capabilityCodes(t, c.file)
			if c.want == "" {
				if len(codes) != 0 {
					t.Fatalf("应通过能力检查，实际 %v", codes)
				}
				return
			}
			for _, code := range codes {
				if code == c.want {
					return
				}
			}
			t.Fatalf("应报 %s，实际 %v", c.want, codes)
		})
	}
}

func TestCapabilityRunsOnlyAfterSourceCheck(t *testing.T) {
	// bad.ts 有类型错误。宿主的 -capability 模式必须先报源码错误、退出 1，
	// 而不是给出能力结论。
	snapshot, err := source.Read(fixturePath(t, "bad.ts"), source.DefaultLimits())
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	diagnostics, err := check(snapshot)
	if err != nil {
		t.Fatalf("源码检查失败：%v", err)
	}
	hasError := false
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			hasError = true
		}
	}
	if !hasError {
		t.Fatal("bad.ts 应当有类型错误")
	}
}
