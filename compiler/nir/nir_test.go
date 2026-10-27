package nir

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func minimalModule() *Module {
	return &Module{
		SchemaVersion: SchemaVersion,
		Entry:         "main",
		Functions: []Function{{
			Name:   "main",
			Params: []Param{},
			Result: TypeVoid,
			Body:   []Stmt{{Op: OpPrint, Value: &Expr{Op: OpNumber, Bits: BitsOf(42)}}},
		}},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	return data
}

func TestRoundTripExample(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "ir", "add.nir.json")
	first, err := LoadFile(path, DefaultLimits())
	if err != nil {
		t.Fatalf("读取示例失败：%v", err)
	}
	data, err := EncodeBytes(first)
	if err != nil {
		t.Fatalf("编码示例失败：%v", err)
	}
	second, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if !reflect.DeepEqual(*first, *second) {
		t.Fatalf("保存后读回不一致：\n原始：%+v\n回读：%+v", *first, *second)
	}
	again, err := EncodeBytes(second)
	if err != nil {
		t.Fatalf("二次编码失败：%v", err)
	}
	if !bytes.Equal(data, again) {
		t.Fatal("同一结构两次编码结果不同，无法用文件内容比较")
	}
}

func TestExampleShape(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "ir", "add.nir.json")
	m, err := LoadFile(path, DefaultLimits())
	if err != nil {
		t.Fatalf("读取示例失败：%v", err)
	}
	if m.Entry != "main" || len(m.Functions) != 2 {
		t.Fatalf("示例结构不符：entry=%q functions=%d", m.Entry, len(m.Functions))
	}
	add := m.Functions[0]
	if add.Name != "add" || add.Result != TypeNumber || len(add.Params) != 2 {
		t.Fatalf("add 签名不符：%+v", add)
	}
	call := m.Functions[1].Body[0].Value
	if call.Op != OpCall || call.Function != "add" || len(call.Args) != 2 {
		t.Fatalf("main 应调用 add：%+v", call)
	}
	a, err := Float64(call.Args[0].Bits)
	if err != nil || a != 40 {
		t.Fatalf("第一个实参应为 40，实际 %v（%v）", a, err)
	}
	b, err := Float64(call.Args[1].Bits)
	if err != nil || b != 2 {
		t.Fatalf("第二个实参应为 2，实际 %v（%v）", b, err)
	}
}

func TestBoolSurvivesEncoding(t *testing.T) {
	for _, want := range []bool{true, false} {
		m := minimalModule()
		m.Functions[0].Body = []Stmt{{Op: OpPrint, Value: &Expr{Op: OpBool, Value: &want}}}
		data, err := EncodeBytes(m)
		if err != nil {
			t.Fatalf("编码 bool %v 失败：%v", want, err)
		}
		// false 必须写出来，不能因为它是零值就被 omitempty 吃掉。
		if !bytes.Contains(data, []byte(`"value": `)) {
			t.Fatalf("编码结果缺少 value 字段：%s", data)
		}
		back, err := DecodeBytes(data, DefaultLimits())
		if err != nil {
			t.Fatalf("回读 bool %v 失败：%v", want, err)
		}
		got := back.Functions[0].Body[0].Value
		if got.Op != OpBool || got.Value == nil || *got.Value != want {
			t.Fatalf("bool %v 保存后读回为 %+v", want, got)
		}
	}
}

func TestBoolRequiresValue(t *testing.T) {
	_, err := DecodeBytes([]byte(`{"schemaVersion":1,"entry":"main","functions":[
		{"name":"main","params":[],"result":"void","body":[{"op":"print","value":{"op":"bool"}}]}]}`), DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "nir.missing") {
		t.Fatalf("缺 value 的 bool 应被拒绝，实际 %v", err)
	}
}

func TestSpecialValuesSurviveEncoding(t *testing.T) {
	cases := []struct {
		name  string
		value float64
	}{
		{"负零", math.Copysign(0, -1)},
		{"NaN", math.NaN()},
		{"正无穷", math.Inf(1)},
		{"负无穷", math.Inf(-1)},
		{"最小非零数", math.SmallestNonzeroFloat64},
		{"普通数值", 42},
	}
	for _, c := range cases {
		bits := BitsOf(c.value)
		back, err := Float64(bits)
		if err != nil {
			t.Fatalf("%s：解析 %s 失败：%v", c.name, bits, err)
		}
		if math.Float64bits(back) != math.Float64bits(c.value) {
			t.Errorf("%s：位模式从 %s 变成 %s", c.name, bits, BitsOf(back))
		}

		m := minimalModule()
		m.Functions[0].Body[0].Value = &Expr{Op: OpNumber, Bits: bits}
		data, err := EncodeBytes(m)
		if err != nil {
			t.Fatalf("%s：编码失败：%v", c.name, err)
		}
		if !bytes.Contains(data, []byte(bits)) {
			t.Errorf("%s：文件里没有保留位串 %s", c.name, bits)
		}
		got, err := DecodeBytes(data, DefaultLimits())
		if err != nil {
			t.Fatalf("%s：回读失败：%v", c.name, err)
		}
		if got.Functions[0].Body[0].Value.Bits != bits {
			t.Errorf("%s：回读位串为 %s，期望 %s", c.name, got.Functions[0].Body[0].Value.Bits, bits)
		}
	}
}

func TestNaNBitPatternKept(t *testing.T) {
	// 静默 NaN 的载荷位不应被改写。
	nan := math.Float64frombits(0x7ff8000000000001)
	if got := BitsOf(nan); got != "0x7ff8000000000001" {
		t.Fatalf("NaN 位模式被改写为 %s", got)
	}
}

func TestBitsRejectsBadStrings(t *testing.T) {
	for _, bits := range []string{"", "0x", "0x1234", "0xzzzzzzzzzzzzzzzz", "0x00000000000000000", "1234"} {
		if _, err := Float64(bits); err == nil {
			t.Errorf("bits %q 应被拒绝", bits)
		}
	}
	if v, err := Float64("0X4044000000000000"); err != nil || v != 40 {
		t.Errorf("大写前缀应被接受：%v %v", v, err)
	}
}

func TestDecodeRejectsInvalidStructure(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module)
		code   string
	}{
		{"坏版本", func(m *Module) { m.SchemaVersion = 2 }, "nir.version"},
		{"缺 entry", func(m *Module) { m.Entry = "" }, "nir.missing"},
		{"没有函数", func(m *Module) { m.Functions = nil }, "nir.missing"},
		{"非法结果类型", func(m *Module) { m.Functions[0].Result = "string" }, "nir.bad-value"},
		{"参数类型不是 number", func(m *Module) { m.Functions[0].Params = []Param{{Name: "a", Type: "string"}} }, "nir.bad-value"},
		{"print 缺 value", func(m *Module) { m.Functions[0].Body[0].Value = nil }, "nir.missing"},
		{"discard 缺 value", func(m *Module) {
			m.Functions[0].Body[0].Op = OpDiscard
			m.Functions[0].Body[0].Value = nil
		}, "nir.missing"},
		{"discard 带 name", func(m *Module) {
			m.Functions[0].Body[0].Op = OpDiscard
			m.Functions[0].Body[0].Name = "x"
		}, "nir.unexpected-field"},
		{"未知语句 op", func(m *Module) { m.Functions[0].Body[0].Op = "jump" }, "nir.bad-op"},
		{"未知表达式 op", func(m *Module) { m.Functions[0].Body[0].Value.Op = "magic" }, "nir.bad-op"},
		{"number 带 name", func(m *Module) { m.Functions[0].Body[0].Value.Name = "x" }, "nir.unexpected-field"},
		{"坏位串", func(m *Module) { m.Functions[0].Body[0].Value.Bits = "0x12" }, "nir.bad-bits"},
		{"缺 bits", func(m *Module) { m.Functions[0].Body[0].Value.Bits = "" }, "nir.missing"},
		{"未知运算符", func(m *Module) {
			m.Functions[0].Body[0].Value = &Expr{
				Op: OpBinary, Operator: "%",
				Left:  &Expr{Op: OpNumber, Bits: BitsOf(1)},
				Right: &Expr{Op: OpNumber, Bits: BitsOf(2)},
			}
		}, "nir.bad-op"},
		{"binary 缺右操作数", func(m *Module) {
			m.Functions[0].Body[0].Value = &Expr{Op: OpBinary, Operator: "+", Left: &Expr{Op: OpNumber, Bits: BitsOf(1)}}
		}, "nir.missing"},
		{"call 缺函数名", func(m *Module) {
			m.Functions[0].Body[0].Value = &Expr{Op: OpCall, Args: []Expr{{Op: OpNumber, Bits: BitsOf(1)}}}
		}, "nir.missing"},
	}
	for _, c := range cases {
		m := minimalModule()
		c.mutate(m)
		got, err := DecodeBytes(mustJSON(t, m), DefaultLimits())
		if err == nil {
			t.Errorf("%s：应被拒绝，却解析成功", c.name)
			continue
		}
		if got != nil {
			t.Errorf("%s：失败时返回了半个模块", c.name)
		}
		if !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s：错误码不是 %s，实际为 %v", c.name, c.code, err)
		}
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	valid := mustJSON(t, minimalModule())
	cases := []struct {
		name  string
		input []byte
	}{
		{"截断输入", valid[:len(valid)/2]},
		{"空输入", nil},
		{"尾随内容", append(append([]byte{}, valid...), []byte("\n{}")...)},
		{"未知字段", []byte(strings.Replace(string(valid), `"entry"`, `"extra":1,"entry"`, 1))},
		{"类型不符", []byte(strings.Replace(string(valid), `"schemaVersion":1`, `"schemaVersion":"1"`, 1))},
	}
	for _, c := range cases {
		got, err := DecodeBytes(c.input, DefaultLimits())
		if err == nil {
			t.Errorf("%s：应被拒绝", c.name)
			continue
		}
		if got != nil {
			t.Errorf("%s：失败时返回了半个模块", c.name)
		}
	}
}

func TestDecodeRejectsOverBudget(t *testing.T) {
	valid := mustJSON(t, minimalModule())

	if _, err := DecodeBytes(valid, Limits{MaxBytes: 16, MaxDepth: 8, MaxNodes: 100, MaxFunctions: 10}); err == nil {
		t.Error("超过字节预算应被拒绝")
	}

	deep := minimalModule()
	expr := &Expr{Op: OpNumber, Bits: BitsOf(1)}
	for i := 0; i < 70; i++ {
		expr = &Expr{Op: OpBinary, Operator: "+", Left: expr, Right: &Expr{Op: OpNumber, Bits: BitsOf(1)}}
	}
	deep.Functions[0].Body[0].Value = expr
	if _, err := DecodeBytes(mustJSON(t, deep), DefaultLimits()); err == nil {
		t.Error("超过深度预算应被拒绝")
	}

	many := minimalModule()
	many.Functions = append(many.Functions, many.Functions[0], many.Functions[0])
	if _, err := DecodeBytes(mustJSON(t, many), Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 100, MaxFunctions: 2}); err == nil {
		t.Error("超过函数数量预算应被拒绝")
	}

	if _, err := DecodeBytes(valid, Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 2, MaxFunctions: 10}); err == nil {
		t.Error("超过节点数预算应被拒绝")
	}

	if _, err := DecodeBytes(valid, Limits{}); err == nil {
		t.Error("非法预算配置应被拒绝")
	}
}

func TestDecodeReadsWithinBudget(t *testing.T) {
	valid := mustJSON(t, minimalModule())
	if _, err := DecodeBytes(valid, DefaultLimits()); err != nil {
		t.Fatalf("合法输入被拒绝：%v", err)
	}
	if _, err := Decode(bytes.NewReader(valid), DefaultLimits()); err != nil {
		t.Fatalf("从 reader 读取失败：%v", err)
	}
}

func TestEncodeRejectsInvalidWithoutWriting(t *testing.T) {
	var buf bytes.Buffer
	m := minimalModule()
	m.SchemaVersion = 9
	if err := Encode(&buf, m); err == nil {
		t.Fatal("非法模块应被拒绝")
	}
	if buf.Len() != 0 {
		t.Fatalf("编码失败时写出了 %d 字节", buf.Len())
	}
}

func TestSaveFileKeepsOldContentOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "module.nir.json")
	if err := os.WriteFile(path, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := minimalModule()
	bad.Entry = ""
	if err := SaveFile(path, bad); err == nil {
		t.Fatal("非法模块应被拒绝")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "旧内容" {
		t.Fatalf("失败时改动了已有文件：%q", data)
	}

	if err := SaveFile(path, minimalModule()); err != nil {
		t.Fatalf("保存合法模块失败：%v", err)
	}
	if _, err := LoadFile(path, DefaultLimits()); err != nil {
		t.Fatalf("保存后读不回来：%v", err)
	}
}

func FuzzDecode(f *testing.F) {
	seed, err := json.Marshal(minimalModule())
	if err != nil {
		f.Fatalf("构造种子失败：%v", err)
	}
	f.Add(seed)
	f.Add([]byte(`{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":[],"result":"void","body":[]}]}`))
	f.Add([]byte(`{"schemaVersion":2}`))
	f.Add([]byte(``))
	f.Add([]byte(`{"schemaVersion":1,"entry":"main","functions":[{"name":"main","params":[],"result":"void","body":[{"op":"print","value":{"op":"number","bits":"0x7ff8000000000000"}}]}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		module, err := DecodeBytes(data, DefaultLimits())
		if err != nil {
			if module != nil {
				t.Fatal("解码失败时返回了模块")
			}
			return
		}
		encoded, err := EncodeBytes(module)
		if err != nil {
			t.Fatalf("编码已解码的模块失败：%v", err)
		}
		again, err := DecodeBytes(encoded, DefaultLimits())
		if err != nil {
			t.Fatalf("回读自己写出的内容失败：%v", err)
		}
		if !reflect.DeepEqual(*module, *again) {
			t.Fatal("往返后结构不一致")
		}
	})
}
