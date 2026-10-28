package frontend

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// sampleFacts 是一份形状正确的事实：一个 add(a, b) 函数、一次调用、一个 const。
func sampleFacts() *Facts {
	number := Type{Kind: KindNumber}
	return &Facts{
		SchemaVersion: 1,
		Session:       "s1",
		File:          "/tmp/add.ts",
		Declarations: []Declaration{
			{ID: 1, Kind: DeclFunction, Name: "add", Start: 0, Length: 40, Result: &number, Parameters: []int{2, 3}},
			{ID: 2, Kind: DeclParameter, Name: "a", Start: 15, Length: 1, Type: &number},
			{ID: 3, Kind: DeclParameter, Name: "b", Start: 28, Length: 1, Type: &number},
			{ID: 4, Kind: DeclConst, Name: "answer", Start: 50, Length: 6, Type: &number},
		},
		References: []Reference{
			{Name: "add", Start: 60, Length: 3, Target: 1},
			{Name: "a", Start: 30, Length: 1, Target: 2},
		},
		Calls: []Call{{
			Start: 60, Length: 12, Callee: 1,
			Arguments: []Type{{Kind: KindNumber}, {Kind: KindNumber}},
			Result:    number,
		}},
		Expressions: []Expression{{Start: 60, Length: 12, Kind: "call", Type: number}},
	}
}

func TestSampleFactsAreValid(t *testing.T) {
	if err := sampleFacts().Validate(); err != nil {
		t.Fatalf("样例事实应当合法：%v", err)
	}
}

func TestValidateRejectsInconsistentFacts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Facts)
		code   string
	}{
		{"重复 ID", func(f *Facts) { f.Declarations[1].ID = 1 }, "facts.duplicate-id"},
		{"引用指向不存在的声明", func(f *Facts) { f.References[0].Target = 99 }, "facts.unknown-target"},
		{"调用指向不存在的声明", func(f *Facts) { f.Calls[0].Callee = 99 }, "facts.unknown-target"},
		{"调用指向常量", func(f *Facts) { f.Calls[0].Callee = 4 }, "facts.call-target"},
		{"实参个数不符", func(f *Facts) { f.Calls[0].Arguments = f.Calls[0].Arguments[:1] }, "facts.arg-count"},
		{"实参类型不符", func(f *Facts) { f.Calls[0].Arguments[0].Kind = KindString }, "facts.arg-type"},
		{"返回类型摘要不符", func(f *Facts) { f.Calls[0].Result.Kind = KindVoid }, "facts.result-type"},
		{"函数缺返回类型", func(f *Facts) { f.Declarations[0].Result = nil }, "facts.bad-type"},
		{"参数缺类型", func(f *Facts) { f.Declarations[1].Type = nil }, "facts.bad-type"},
		{"参数重复归属", func(f *Facts) { f.Declarations[0].Parameters = []int{2, 2} }, "facts.shared-parameter"},
		{"参数没有归属", func(f *Facts) { f.Declarations[0].Parameters = []int{2} }, "facts.orphan-parameter"},
		{"参数 ID 指向函数", func(f *Facts) { f.Declarations[0].Parameters = []int{1} }, "facts.bad-parameter"},
		{"声明种类不认识", func(f *Facts) { f.Declarations[1].Kind = "variable" }, "facts.bad-decl-kind"},
		{"可变性与种类不符", func(f *Facts) { f.Declarations[1].Mutable = true }, "facts.bad-mutable"},
		{"类型类别带空格", func(f *Facts) { f.Declarations[1].Type.Kind = "number " }, "facts.bad-type"},
		{"类型类别大小写不符", func(f *Facts) { f.Declarations[1].Type.Kind = "Number" }, "facts.bad-type"},
		{"位置为负", func(f *Facts) { f.References[0].Start = -1 }, "facts.bad-position"},
		{"缺会话标识", func(f *Facts) { f.Session = "" }, "facts.no-session"},
		{"schema 版本不符", func(f *Facts) { f.SchemaVersion = 2 }, "facts.schema"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := sampleFacts()
			c.mutate(f)
			err := f.Validate()
			if err == nil {
				t.Fatal("应当被拒绝")
			}
			var d *diagnostic.Diagnostic
			if !errors.As(err, &d) || d.Code != c.code {
				t.Fatalf("错误码应为 %s，实际 %v", c.code, err)
			}
			if d.Category != diagnostic.Internal {
				t.Fatalf("事实不一致属于内部故障，实际类别 %v", d.Category)
			}
		})
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	data, err := json.Marshal(sampleFacts())
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if _, err := Decode(data); err != nil {
		t.Fatalf("合法事实应能解码：%v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	raw["upstreamSymbol"] = "0xdeadbeef"
	forged, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var d *diagnostic.Diagnostic
	if err := func() error { _, err := Decode(forged); return err }(); !errors.As(err, &d) || d.Code != "facts.decode" {
		t.Fatalf("未知字段应被拒绝，实际 %v", err)
	}
}

func TestCombineRejectsDifferentSessions(t *testing.T) {
	a := sampleFacts()
	b := sampleFacts()
	b.Session = "s2"
	if _, err := Combine(a, b); err == nil {
		t.Fatal("不同会话的事实不能合并")
	} else {
		var d *diagnostic.Diagnostic
		if !errors.As(err, &d) || d.Code != "facts.session-mismatch" {
			t.Fatalf("错误码应为 facts.session-mismatch，实际 %v", err)
		}
	}

	same := sampleFacts()
	same.Declarations = nil
	same.References = nil
	same.Calls = nil
	same.Expressions = nil
	merged, err := Combine(a, same)
	if err != nil {
		t.Fatalf("同一会话应能合并：%v", err)
	}
	if len(merged.Declarations) != len(a.Declarations) {
		t.Fatalf("合并后声明数应为 %d，实际 %d", len(a.Declarations), len(merged.Declarations))
	}
}

// TestFactsHoldOnlyPlainData 是数据结构依赖检查：事实的字段只能是基本类型、
// 本包定义的结构体，或它们组成的切片；指针只允许指向本包的结构体。
//
// 它拦的是「顺手把上游 *ast.Node 塞进事实」这类改动——那种字段会让事实依赖
// 一次具体的检查会话，会话关掉之后就不能读了。
func TestFactsHoldOnlyPlainData(t *testing.T) {
	allowed := reflect.TypeOf(Facts{}).PkgPath()
	seen := make(map[reflect.Type]bool)

	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		if seen[rt] {
			return
		}
		seen[rt] = true
		switch rt.Kind() {
		case reflect.Bool, reflect.Int, reflect.String:
			return
		case reflect.Slice:
			walk(rt.Elem(), path+"[]")
		case reflect.Ptr:
			if rt.Elem().Kind() != reflect.Struct {
				t.Fatalf("%s 是指针，指向 %s", path, rt.Elem())
			}
			walk(rt.Elem(), path)
		case reflect.Struct:
			if rt.PkgPath() != allowed {
				t.Fatalf("%s 引用了本包之外的类型 %s", path, rt)
			}
			for i := 0; i < rt.NumField(); i++ {
				field := rt.Field(i)
				if field.PkgPath != "" {
					continue
				}
				walk(field.Type, path+"."+field.Name)
			}
		default:
			t.Fatalf("%s 的类型 %s 不是纯数据", path, rt)
		}
	}
	walk(reflect.TypeOf(Facts{}), "Facts")
}
