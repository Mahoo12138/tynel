package diagnostic

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCategoryExitCodes(t *testing.T) {
	cases := []struct {
		category Category
		exit     int
		name     string
	}{
		{Source, 1, "源码错误"},
		{Usage, 2, "用法错误"},
		{Capability, 3, "能力限制"},
		{Environment, 4, "环境缺失"},
		{Internal, 5, "内部故障"},
	}
	for _, c := range cases {
		if got := c.category.ExitCode(); got != c.exit {
			t.Errorf("%v 的退出状态为 %d，期望 %d", c.category, got, c.exit)
		}
		if got := c.category.String(); got != c.name {
			t.Errorf("类别 %d 的名称为 %q，期望 %q", c.category, got, c.name)
		}
	}
}

func TestExitCodeMapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"空错误", nil, ExitOK},
		{"普通错误按内部故障", errors.New("boom"), ExitInternal},
		{"诊断按类别", New(Capability, "cap.unsupported", "暂不支持"), ExitCapability},
		{"包装后的诊断仍可识别", fmt.Errorf("上层包装: %w", New(Environment, "tool.missing", "缺工具")), ExitEnvironment},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("%s：退出状态为 %d，期望 %d", c.name, got, c.want)
		}
	}
}

func TestWriteToFormat(t *testing.T) {
	var buf bytes.Buffer
	d := New(Environment, "tool.missing", "找不到工具 %s", "go").
		At("examples/add.ts", 3, 5).
		WithDetail("line1\nline2\n")
	if err := Write(&buf, d); err != nil {
		t.Fatalf("Write 失败：%v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"tynel: 环境缺失 (tool.missing): 找不到工具 go",
		"  位置: examples/add.ts:3:5",
		"  原因: line1\n    line2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q，实际为：\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("输出应以换行结束：%q", out)
	}
}

func TestWriteToWithoutPosition(t *testing.T) {
	var buf bytes.Buffer
	d := New(Usage, "", "缺少命令")
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo 失败：%v", err)
	}
	out := buf.String()
	if strings.Contains(out, "位置") {
		t.Errorf("没有位置时不应输出位置行：%q", out)
	}
	if !strings.Contains(out, "tynel: 用法错误: 缺少命令") {
		t.Errorf("没有代码时不应输出括号：%q", out)
	}
}

func TestWriteWrapsPlainError(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, errors.New("boom")); err != nil {
		t.Fatalf("Write 失败：%v", err)
	}
	if got := buf.String(); !strings.Contains(got, "内部故障") || !strings.Contains(got, "boom") {
		t.Errorf("普通错误应包装为内部故障并保留原因，实际为：%q", got)
	}
}

func TestWriteNilErrorWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, nil); err != nil {
		t.Fatalf("Write 失败：%v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("空错误不应输出内容，实际为：%q", buf.String())
	}
}
