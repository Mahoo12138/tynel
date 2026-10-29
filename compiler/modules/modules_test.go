package modules

import (
	"errors"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
)

func code(err error) string {
	var d *diagnostic.Diagnostic
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func facts(path string, decls ...frontend.Declaration) *frontend.Facts {
	return &frontend.Facts{ModulePath: path, File: path, Declarations: decls}
}

func TestBuildResolvesRenamedImports(t *testing.T) {
	a := facts("a.ts",
		frontend.Declaration{ID: 1, Module: "a.ts", Kind: frontend.DeclFunction, Name: "helper", Start: 0, Length: 10},
		frontend.Declaration{ID: 2, Module: "a.ts", Kind: frontend.DeclConst, Name: "base", Start: 11, Length: 10},
	)
	a.Exports = []frontend.Export{{Name: "helper", ID: 1, Start: 0, Length: 10}, {Name: "base", ID: 2, Start: 11, Length: 10}}
	b := facts("b.ts",
		frontend.Declaration{ID: 1, Module: "b.ts", Kind: frontend.DeclFunction, Name: "helper", Start: 0, Length: 10},
	)
	b.Exports = []frontend.Export{{Name: "helper", ID: 1, Start: 0, Length: 10}}
	main := facts("main.ts")
	main.Imports = []frontend.Import{
		{Local: "h", Imported: "helper", Module: "a.ts", Start: 0, Length: 1},
		{Local: "h2", Imported: "helper", Module: "b.ts", Start: 2, Length: 1},
		{Local: "base", Imported: "base", Module: "a.ts", Start: 4, Length: 1},
	}

	table, err := Build(main, []*frontend.Facts{a, b})
	if err != nil {
		t.Fatalf("建立符号表失败：%v", err)
	}
	h, ok := table.Resolve("h")
	if !ok || h.Module != "a.ts" || h.Name != "helper" {
		t.Fatalf("h 解析为 %+v", h)
	}
	h2, ok := table.Resolve("h2")
	if !ok || h2.Module != "b.ts" || h2.Name != "helper" {
		t.Fatalf("h2 解析为 %+v", h2)
	}
	base, ok := table.Resolve("base")
	if !ok || base.Name != "base" {
		t.Fatalf("base 解析为 %+v", base)
	}
}

func TestBuildRejectsMissingExport(t *testing.T) {
	a := facts("a.ts")
	main := facts("main.ts")
	main.Imports = []frontend.Import{{Local: "nope", Imported: "nope", Module: "a.ts", Start: 0, Length: 1}}
	_, err := Build(main, []*frontend.Facts{a})
	if code(err) != "module.missing-export" {
		t.Fatalf("错误为 %v，期望 module.missing-export", err)
	}
}

func TestBuildRejectsMissingModule(t *testing.T) {
	main := facts("main.ts")
	main.Imports = []frontend.Import{{Local: "x", Imported: "x", Module: "forged.ts", Start: 0, Length: 1}}
	_, err := Build(main, nil)
	if code(err) != "module.missing-module" {
		t.Fatalf("错误为 %v，期望 module.missing-module", err)
	}
}

func TestBuildRejectsDuplicateExport(t *testing.T) {
	a := facts("a.ts")
	a.Exports = []frontend.Export{
		{Name: "helper", ID: 1, Start: 0, Length: 10},
		{Name: "helper", ID: 2, Start: 11, Length: 10},
	}
	main := facts("main.ts")
	_, err := Build(main, []*frontend.Facts{a})
	if code(err) != "module.duplicate-export" {
		t.Fatalf("错误为 %v，期望 module.duplicate-export", err)
	}
}

func TestBuildRejectsCapturedModuleValue(t *testing.T) {
	a := facts("a.ts",
		frontend.Declaration{ID: 1, Module: "a.ts", Kind: frontend.DeclConst, Name: "offset", Start: 0, Length: 5},
		frontend.Declaration{ID: 2, Module: "a.ts", Kind: frontend.DeclFunction, Name: "add", Start: 10, Length: 20,
			Body: &frontend.Node{Kind: "Block", Start: 10, Length: 20}},
	)
	a.Exports = []frontend.Export{{Name: "add", ID: 2, Start: 10, Length: 20}}
	a.References = []frontend.Reference{{Name: "offset", Start: 15, Length: 6, Target: 1, TargetModule: "a.ts", TargetName: "offset"}}
	main := facts("main.ts")
	main.Imports = []frontend.Import{{Local: "add", Imported: "add", Module: "a.ts", Start: 0, Length: 1}}
	_, err := Build(main, []*frontend.Facts{a})
	if code(err) != "module.captured-variable" {
		t.Fatalf("错误为 %v，期望 module.captured-variable", err)
	}
}
