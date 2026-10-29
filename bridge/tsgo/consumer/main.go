// Command consumer 是一个位于 Tynel 之外的原生宿主示例。
//
// 它做三件事：把源文件读成一次快照、通过适配包检查这份快照、把前端给的字节
// 偏移映射成行列。除了适配包，它只依赖 Tynel 自己的 compiler/source，不导入
// 上游任何 internal 包——这正是适配包存在的理由。
//
//	consumer <file.ts>             输出诊断 JSON；有 error 级诊断时退出 1
//	consumer -quiet <file.ts>      只输出摘要
//	consumer -facts <file.ts>      输出前端事实 JSON（开发接口）
//	consumer -capability <file.ts> 先查源码，再查原生能力；有越界写法时退出 3
//	consumer -nir <file.ts>       输出降低后的 NIR（开发接口）
//	consumer -pipeline -want=<check|build|facts|nir> <file.ts>
//	                              输出主命令行使用的一次性结果；只要产出了结果
//	                              就退出 0，结论由结果里的诊断与能力项表达
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mahoo12138/tynel/compiler/capability"
	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/lower"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/compiler/source"
	"github.com/microsoft/typescript-go/tyneladapter"
)

func main() {
	quiet := flag.Bool("quiet", false, "只输出摘要")
	facts := flag.Bool("facts", false, "输出前端事实而不是诊断")
	capabilityCheck := flag.Bool("capability", false, "检查程序是否落在原生子集内")
	nirOut := flag.Bool("nir", false, "输出降低后的 NIR")
	importsOut := flag.Bool("imports", false, "输出入口文件的导入声明")
	pipelineOut := flag.Bool("pipeline", false, "输出主命令行使用的一次性结果")
	want := flag.String("want", "check", "pipeline 附带的内容：check、build、facts 或 nir")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：consumer [-quiet] <file.ts>")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(diagnostic.ExitUsage)
	}
	path, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法解析路径：%v\n", err)
		os.Exit(diagnostic.ExitUsage)
	}

	// 入口只读一次。依赖文件也在这里读成快照，后续不再访问磁盘。
	snapshot, err := source.Read(path, source.DefaultLimits())
	if err != nil {
		report(err)
	}
	var deps []*source.Snapshot
	for _, arg := range flag.Args()[1:] {
		depPath, err := filepath.Abs(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法解析依赖路径：%v\n", err)
			os.Exit(diagnostic.ExitUsage)
		}
		dep, err := source.Read(depPath, source.DefaultLimits())
		if err != nil {
			report(err)
		}
		deps = append(deps, dep)
	}

	if *pipelineOut {
		runPipeline(snapshot, deps, *want)
		return
	}

	if *facts {
		out, err := dumpFacts(snapshot)
		if err != nil {
			report(err)
		}
		if _, err := os.Stdout.Write(out); err != nil {
			fmt.Fprintf(os.Stderr, "写出事实失败：%v\n", err)
			os.Exit(diagnostic.ExitInternal)
		}
		return
	}

	if *importsOut {
		runImports(snapshot)
		return
	}

	if *capabilityCheck {
		runCapability(snapshot)
		return
	}

	if *nirOut {
		runLower(snapshot)
		return
	}

	diagnostics, err := checkFiles(snapshot, deps)
	if err != nil {
		report(err)
	}

	errors := 0
	items := make([]pipeline.Diagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			errors++
		}
		items = append(items, flatten(d))
	}

	if *quiet {
		fmt.Printf("%d 条诊断，其中 %d 条错误\n", len(items), errors)
	} else {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(items); err != nil {
			fmt.Fprintf(os.Stderr, "写出诊断失败：%v\n", err)
			os.Exit(diagnostic.ExitInternal)
		}
	}
	if errors > 0 {
		os.Exit(diagnostic.ExitSource)
	}
}

// runPipeline 一次走完检查、能力判断和降低，把结果整体写给主命令行。
//
// 与 -capability、-nir 不同，它总是退出 0：结论写在结果里（诊断、能力项、
// 是否通过验证），调用方按内容决定退出状态。这样一份结果只有一个来源，不会
// 出现「JSON 说失败但退出 0」之类的分裂。只有真正取不到结果时才非零退出。
func runPipeline(snapshot *source.Snapshot, deps []*source.Snapshot, want string) {
	switch want {
	case "check", "build", "facts", "nir":
	default:
		fmt.Fprintf(os.Stderr, "want 只接受 check、build、facts 或 nir，收到 %q\n", want)
		os.Exit(diagnostic.ExitUsage)
	}

	result := &pipeline.Result{
		SchemaVersion: pipeline.SchemaVersion,
		File:          snapshot.Path(),
		SourceSHA256:  sha256Hex(snapshot.Bytes()),
		SourceBytes:   len(snapshot.Bytes()),
		Diagnostics:   []pipeline.Diagnostic{},
		Capability:    []pipeline.Finding{},
	}

	diagnostics, err := checkFiles(snapshot, deps)
	if err != nil {
		report(err)
	}
	for _, d := range diagnostics {
		result.Diagnostics = append(result.Diagnostics, flatten(d))
	}
	if result.HasSourceErrors() {
		writeJSON(result)
		return
	}

	facts, err := factsOfFiles(snapshot, deps)
	if err != nil {
		report(err)
	}
	if want == "facts" {
		result.Facts = facts
	}

	findings, err := capability.Check(snapshot, facts)
	if err != nil {
		report(err)
	}
	for _, d := range findings {
		result.Capability = append(result.Capability, pipeline.Finding{
			File:     d.File,
			Line:     d.Line,
			Column:   d.Column,
			Category: d.Category.String(),
			Code:     d.Code,
			Message:  d.Message,
		})
	}
	if len(findings) > 0 {
		writeJSON(result)
		return
	}

	module, err := lower.Lower(snapshot, facts)
	if err != nil {
		report(err)
	}
	result.Summary.Functions = len(module.Functions)
	result.Summary.Verified = true
	if want == "build" || want == "nir" {
		result.NIR = module
	}
	writeJSON(result)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// runCapability 先做源码检查，再做能力检查。
//
// 源码有错时能力结论没有意义——类型都没定下来，「这个类型有没有原生表示」
// 无从谈起。所以顺序固定：源码错误退出 1，能力越界退出 3，全部通过退出 0。
func runCapability(snapshot *source.Snapshot) {
	gate(snapshot)
	emitFindings(nil)
}

// runLower 走完整的前半程：源码检查、能力检查、降低，然后输出 NIR。
//
// 降低在会话关闭之后进行——gate 里取的 facts 已经是普通数据，不再依赖
// 任何前端句柄。
func runLower(snapshot *source.Snapshot) {
	facts := gate(snapshot)
	module, err := lower.Lower(snapshot, facts)
	if err != nil {
		report(err)
	}
	data, err := nir.EncodeBytes(module)
	if err != nil {
		report(err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "写出 NIR 失败：%v\n", err)
		os.Exit(diagnostic.ExitInternal)
	}
}

// gate 是进入后端之前的关卡：源码错误退出 1，能力越界退出 3，通过则返回事实。
func gate(snapshot *source.Snapshot) *frontend.Facts {
	diagnostics, err := check(snapshot)
	if err != nil {
		report(err)
	}
	for _, d := range diagnostics {
		if d.Severity == source.SeverityError {
			emitDiagnostics(diagnostics)
			os.Exit(diagnostic.ExitSource)
		}
	}
	facts, err := factsOf(snapshot)
	if err != nil {
		report(err)
	}
	findings, err := capability.Check(snapshot, facts)
	if err != nil {
		report(err)
	}
	if len(findings) > 0 {
		emitFindings(findings)
		os.Exit(diagnostic.ExitCapability)
	}
	return facts
}

// runImports 输出入口文件的导入声明，供依赖图构建器使用。它只读语法树，
// 不执行检查，也不尝试解析文件系统。
func runImports(snapshot *source.Snapshot) {
	session, err := tyneladapter.Open(tyneladapter.Options{
		FileName: snapshot.Path(),
		Source:   snapshot.Bytes(),
	})
	if err != nil {
		report(diagnostic.New(diagnostic.Environment, "frontend.session", "无法建立检查会话").WithDetail(err.Error()))
	}
	imports, err := session.Imports()
	closeErr := session.Close()
	if err != nil {
		report(diagnostic.New(diagnostic.Internal, "frontend.imports", "读取导入声明失败").WithDetail(err.Error()))
	}
	if closeErr != nil {
		report(diagnostic.New(diagnostic.Internal, "frontend.close", "关闭会话失败").WithDetail(closeErr.Error()))
	}
	writeJSON(imports)
}

// openSession 把入口和依赖一起放进程序；依赖只用于类型解析，不单独输出。
func openSession(entry *source.Snapshot, deps []*source.Snapshot) (*tyneladapter.Session, error) {
	inputs := make([]tyneladapter.Input, 0, len(deps)+1)
	inputs = append(inputs, tyneladapter.Input{FileName: entry.Path(), Source: entry.Bytes()})
	for _, dep := range deps {
		inputs = append(inputs, tyneladapter.Input{FileName: dep.Path(), Source: dep.Bytes()})
	}
	return tyneladapter.Open(tyneladapter.Options{
		FileName: entry.Path(),
		Source:   entry.Bytes(),
		Files:    inputs,
	})
}

// factsOf 取事实并解码校验，供 -facts 与 -capability 共用。
func factsOf(snapshot *source.Snapshot) (*frontend.Facts, error) {
	return factsOfFiles(snapshot, nil)
}

func factsOfFiles(snapshot *source.Snapshot, deps []*source.Snapshot) (*frontend.Facts, error) {
	session, err := openSession(snapshot, deps)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "frontend.session", "无法建立检查会话").WithDetail(err.Error())
	}
	raw, err := session.Facts()
	closeErr := session.Close()
	if err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.facts", "读取前端事实失败").WithDetail(err.Error())
	}
	if closeErr != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.close", "关闭会话失败").WithDetail(closeErr.Error())
	}
	return frontend.Decode(raw)
}

func emitFindings(findings []*diagnostic.Diagnostic) {
	items := make([]pipeline.Finding, 0, len(findings))
	for _, d := range findings {
		items = append(items, pipeline.Finding{
			File:     d.File,
			Line:     d.Line,
			Column:   d.Column,
			Category: d.Category.String(),
			Code:     d.Code,
			Message:  d.Message,
		})
	}
	writeJSON(items)
}

func emitDiagnostics(diagnostics []source.Diagnostic) {
	items := make([]pipeline.Diagnostic, 0, len(diagnostics))
	for _, d := range diagnostics {
		items = append(items, flatten(d))
	}
	writeJSON(items)
}

func writeJSON(v any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "写出结果失败：%v\n", err)
		os.Exit(diagnostic.ExitInternal)
	}
}

// dumpFacts 取事实、解码并校验，然后按规范形式重新编码输出。
//
// 输出的是解码后的事实，不是适配层的原始字节：解码会拒绝未知字段，校验会
// 检查 ID、引用和调用是否自洽，能通过这两关的数据才值得给别人看。
func dumpFacts(snapshot *source.Snapshot) ([]byte, error) {
	decoded, err := factsOf(snapshot)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(decoded, "", "  ")
}

// check 建立会话、取诊断、关闭会话，再把结果映射到快照位置。
func check(snapshot *source.Snapshot) ([]source.Diagnostic, error) {
	return checkFiles(snapshot, nil)
}

func checkFiles(snapshot *source.Snapshot, deps []*source.Snapshot) ([]source.Diagnostic, error) {
	session, err := openSession(snapshot, deps)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "frontend.session", "无法建立检查会话").WithDetail(err.Error())
	}
	raw, err := session.Diagnostics()
	closeErr := session.Close()
	if err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.diagnostics", "读取前端诊断失败").WithDetail(err.Error())
	}
	if closeErr != nil {
		return nil, diagnostic.New(diagnostic.Internal, "frontend.close", "关闭会话失败").WithDetail(closeErr.Error())
	}
	return snapshot.MapDiagnostics(convert(raw))
}

// convert 把适配层的诊断摊平成 source 认识的形状。这是宿主承担的转换，
// 适配层不需要知道 Tynel 的诊断结构。
func convert(in []tyneladapter.Diagnostic) []source.FrontendDiagnostic {
	out := make([]source.FrontendDiagnostic, 0, len(in))
	for _, d := range in {
		out = append(out, source.FrontendDiagnostic{
			File:     d.File,
			Code:     d.Code,
			Category: d.Category,
			Message:  d.Message,
			Start:    d.Start,
			Length:   d.Length,
		})
	}
	return out
}

func flatten(d source.Diagnostic) pipeline.Diagnostic {
	return pipeline.Diagnostic{
		File:      d.Location.Path,
		Line:      d.Location.Start.Line,
		Column:    d.Location.Start.Column,
		EndLine:   d.Location.End.Line,
		EndColumn: d.Location.End.Column,
		Start:     d.Location.Bytes.Start,
		Length:    d.Location.Bytes.End - d.Location.Bytes.Start,
		Severity:  string(d.Severity),
		Code:      d.Code,
		Message:   d.Message,
	}
}

// report 输出一条失败并按其类别退出。源码错误与环境缺失在这里分开。
func report(err error) {
	_ = diagnostic.Write(os.Stderr, err)
	os.Exit(diagnostic.ExitCode(err))
}
