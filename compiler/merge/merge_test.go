package merge

import (
	"testing"

	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/source"
)

func snapshot(t *testing.T, path string) *source.Snapshot {
	t.Helper()
	s, err := source.New(path, []byte("// "+path+"\n"), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func numberFunction(module string, id int, name string) frontend.Declaration {
	number := frontend.Type{Kind: frontend.KindNumber}
	body := frontend.Node{Kind: "Block", Start: 0, Length: 20, Children: []frontend.Node{
		{Kind: "ReturnStatement", Start: 10, Length: 9, Children: []frontend.Node{
			{Kind: "NumericLiteral", Start: 17, Length: 1, Bits: "0x3ff0000000000000", Type: &number},
		}},
	}}
	return frontend.Declaration{ID: id, Module: module, Kind: frontend.DeclFunction, Name: name, Start: 0, Length: 20, Result: &number, Body: &body}
}

func TestBuildPrefixesFunctionsAndRemapsCalls(t *testing.T) {
	a := &frontend.Facts{
		SchemaVersion: 1, Session: "a", File: "a.ts", ModulePath: "a.ts",
		Declarations: []frontend.Declaration{numberFunction("a.ts", 1, "helper")},
		Exports:      []frontend.Export{{Name: "helper", ID: 1, Start: 0, Length: 10}},
	}
	main := &frontend.Facts{
		SchemaVersion: 1, Session: "b", File: "main.ts", ModulePath: "main.ts",
		Declarations: []frontend.Declaration{numberFunction("main.ts", 1, "run")},
		Imports:      []frontend.Import{{Local: "h", Imported: "helper", Module: "a.ts", Start: 0, Length: 1}},
		Calls:        []frontend.Call{{Start: 10, Length: 3, CalleeModule: "a.ts", CalleeName: "helper", Result: frontend.Type{Kind: frontend.KindNumber}}},
	}
	merged, err := Build([]Unit{{Snapshot: snapshot(t, "main.ts"), Facts: main}, {Snapshot: snapshot(t, "a.ts"), Facts: a}}, []string{"a.ts", "main.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Facts.Declarations) != 2 {
		t.Fatalf("声明数为 %d", len(merged.Facts.Declarations))
	}
	if merged.Facts.Declarations[0].Name == merged.Facts.Declarations[1].Name {
		t.Fatalf("函数名没有模块前缀：%+v", merged.Facts.Declarations)
	}
	if merged.Facts.Calls[0].Callee == 0 {
		t.Fatalf("跨模块调用没有重映射：%+v", merged.Facts.Calls[0])
	}
}
