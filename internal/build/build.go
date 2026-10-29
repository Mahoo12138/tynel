// Package build 编排一次「NIR 到原生程序」的构建。
//
// 它固定一次输入快照，把工具调用交给可替换的 Runner，构建产物先写到目标
// 目录的临时文件，成功后才改名发布；失败时不覆盖已有的成功产物。构建本身
// 不运行用户程序。
package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// 仓库内的固定位置。
const (
	SupportDir   = "bridge/go/support"
	ToolexecPath = "bridge/go/toolexec"
	CompilerPath = ".work/bridge/compile"
	StampPath    = ".work/bridge/stamp.json"
	SnapshotDir  = ".work/build/snapshots"
)

// Options 是一次构建的输入。
//
// Input 与 InputData 二选一：前者读文件，后者直接用内存里的 NIR 字节。从
// TypeScript 构建时降低结果在内存里，没必要先落一份用户可见的文件；快照仍会
// 写到 .work 下，编译器读的始终是快照。
type Options struct {
	Root      string  // 项目根
	Input     string  // NIR 文件
	InputData []byte  // 内存中的 NIR；非空时忽略 Input
	Output    string  // 产物路径
	Source    *Source // 可选：源码身份，写进构建记录
	GraphHash string  // 可选：依赖图内容哈希，写进构建记录
}

// Source 是产生这份 NIR 的源码。从 NIR 文件构建时没有这一项。
type Source struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// Record 是构建记录，成功后写到 <产物>.build.json。
type Record struct {
	SchemaVersion  int     `json:"schemaVersion"`
	StartedAt      string  `json:"startedAt"`
	DurationMillis int64   `json:"durationMillis"`
	Source         *Source `json:"source,omitempty"`
	GraphHash      string  `json:"graphHash,omitempty"`
	Input          struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	} `json:"input"`
	Toolchain struct {
		Version string `json:"version"`
		Target  string `json:"target"`
	} `json:"toolchain"`
	Tools struct {
		Go             string `json:"go"`
		Compiler       string `json:"compiler"`
		CompilerSHA256 string `json:"compilerSHA256"`
		Toolexec       string `json:"toolexec"`
		BuildTag       string `json:"buildTag"`
		Snapshot       string `json:"snapshot"`
	} `json:"tools"`
	Output struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	} `json:"output"`
}

// Run 执行一次构建。失败时返回诊断，且不改动目标位置已有的文件。
func Run(opts Options, runner toolchain.Runner) (*Record, error) {
	started := time.Now()
	if opts.Root == "" || opts.Output == "" || (len(opts.InputData) == 0 && opts.Input == "") {
		return nil, diagnostic.New(diagnostic.Usage, "build.missing-argument", "构建需要项目根、NIR 输入和输出路径")
	}

	// 1. 输入快照：一次读入，后续都用这份字节，构建期间改源文件不影响本次结果。
	data := opts.InputData
	inputPath := opts.Input
	if len(data) == 0 {
		read, err := os.ReadFile(opts.Input)
		if err != nil {
			return nil, diagnostic.New(diagnostic.Environment, "build.input-unreadable", "读取 NIR %s 失败", opts.Input).WithDetail(err.Error())
		}
		data = read
	}
	inputSHA := sha256Hex(data)
	module, err := nir.DecodeBytes(data, nir.DefaultLimits())
	if err != nil {
		return nil, err
	}
	if err := nir.Verify(module); err != nil {
		return nil, err
	}

	// 2. 工具：隔离 SDK 的 go、修改版 compiler、工具包装。
	report, err := toolchain.LoadReport(filepath.Join(opts.Root, filepath.FromSlash(toolchain.ReportPath)))
	if err != nil {
		return nil, err
	}
	sdk, ok := report.SDK()
	if !ok {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-sdk", "准备报告里没有 SDK 记录；重新运行 ./tools/toolchain/prep")
	}
	goTool := filepath.Join(sdk.Dir, "bin", "go")
	compiler := filepath.Join(opts.Root, filepath.FromSlash(CompilerPath))
	compilerSHA, err := hashFile(compiler)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.no-compiler", "找不到修改版编译器 %s；先运行 ./bridge/go/build", CompilerPath).WithDetail(err.Error())
	}
	if err := checkStamp(opts.Root, sdk.Dir, compilerSHA); err != nil {
		return nil, err
	}

	// 3. 快照落盘：编译器读的是这份文件，不是用户路径。
	snapshotDir := filepath.Join(opts.Root, filepath.FromSlash(SnapshotDir))
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.snapshot-failed", "无法创建快照目录").WithDetail(err.Error())
	}
	snapshot := filepath.Join(snapshotDir, inputSHA[:16]+".nir.json")
	if existing, err := os.ReadFile(snapshot); err == nil {
		// 快照按内容哈希命名。名字对得上但内容对不上，说明它被别的程序改过，
		// 这时宁可停下，也不要悄悄覆盖后继续构建。
		if sha256Hex(existing) != inputSHA {
			return nil, diagnostic.New(diagnostic.Environment, "build.snapshot-corrupt", "输入快照 %s 的内容与文件名不符", snapshot)
		}
	} else if !os.IsNotExist(err) {
		return nil, diagnostic.New(diagnostic.Environment, "build.snapshot-failed", "无法读取输入快照").WithDetail(err.Error())
	} else if err := os.WriteFile(snapshot, data, 0o644); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.snapshot-failed", "无法写入输入快照").WithDetail(err.Error())
	}
	if inputPath == "" {
		// 内存里的 NIR 没有用户可见的文件路径；记录里写实际被编译的快照，
		// 而不是一个不存在的名字。
		inputPath = snapshot
	}

	// 4. 输出先写到同目录临时文件。
	outDir := filepath.Dir(opts.Output)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法创建输出目录 %s", outDir).WithDetail(err.Error())
	}
	tmpFile, err := os.CreateTemp(outDir, ".tynel-build-*")
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法在 %s 创建临时文件", outDir).WithDetail(err.Error())
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	os.Remove(tmpPath)

	// 5. 构建：Go 缓存键不含 NIR 与 bridge 身份，用 build tag 区分。
	tag := "tynel_" + inputSHA[:8] + "_" + compilerSHA[:8]
	env := append(os.Environ(),
		"TYNEL_NIR="+snapshot,
		"TYNEL_COMPILE="+compiler,
	)
	args := []string{
		"-C", opts.Root,
		"build",
		"-toolexec=" + filepath.Join(opts.Root, filepath.FromSlash(ToolexecPath)),
		"-tags", "tynelapp " + tag,
		"-o", tmpPath,
		"./" + SupportDir,
	}
	out, err := runner.RunEnv(env, goTool, args...)
	if err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Environment, "build.tool-missing", "无法启动 %s", goTool).WithDetail(err.Error())
	}
	if out.ExitCode != 0 {
		os.Remove(tmpPath)
		return nil, out.Failure("go", args)
	}

	// 6. 产物存在才发布。改名是原子的，失败时旧产物保持原样。
	info, err := os.Stat(tmpPath)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "build.no-output", "构建报告成功但没有产出文件").WithDetail(err.Error())
	}
	outputSHA, err := hashFile(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unreadable", "无法读取构建产物").WithDetail(err.Error())
	}
	if err := os.Rename(tmpPath, opts.Output); err != nil {
		os.Remove(tmpPath)
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法发布到 %s", opts.Output).WithDetail(err.Error())
	}

	// 7. 构建记录。
	record := &Record{SchemaVersion: 1, StartedAt: started.Format(time.RFC3339)}
	record.DurationMillis = time.Since(started).Milliseconds()
	record.Source = opts.Source
	record.GraphHash = opts.GraphHash
	record.Input.Path = inputPath
	record.Input.SHA256 = inputSHA
	record.Input.Bytes = len(data)
	record.Toolchain.Version = report.LockedVersion
	record.Toolchain.Target = report.Target
	record.Tools.Go = goTool
	record.Tools.Compiler = compiler
	record.Tools.CompilerSHA256 = compilerSHA
	record.Tools.Toolexec = filepath.Join(opts.Root, filepath.FromSlash(ToolexecPath))
	record.Tools.BuildTag = tag
	record.Tools.Snapshot = snapshot
	record.Output.Path = opts.Output
	record.Output.SHA256 = outputSHA
	record.Output.Bytes = info.Size()
	if err := writeRecord(opts.Output+".build.json", record); err != nil {
		// 没有记录的产物无法复核来源与哈希，留着比没有更危险：删掉它，
		// 让这次构建明确失败。
		os.Remove(opts.Output)
		return nil, err
	}
	return record, nil
}

// RecordPath 返回某个产物对应的构建记录路径。
func RecordPath(output string) string { return output + ".build.json" }

type stamp struct {
	SchemaVersion  int               `json:"schemaVersion"`
	CompilerSHA256 string            `json:"compilerSHA256"`
	SDKVersion     string            `json:"sdkVersion"`
	BuiltAt        string            `json:"builtAt"`
	Sources        map[string]string `json:"sources"`
}

// checkStamp 确认修改版编译器与当前 bridge 源码、SDK 源码一致。过期编译器
// 直接拒绝，避免用旧 bridge 生成产物。
func checkStamp(root, sdkDir, compilerSHA string) error {
	path := filepath.Join(root, filepath.FromSlash(StampPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "build.no-stamp", "找不到编译器戳记 %s；先运行 ./bridge/go/build", StampPath).WithDetail(err.Error())
	}
	var st stamp
	if err := json.Unmarshal(data, &st); err != nil {
		return diagnostic.New(diagnostic.Environment, "build.bad-stamp", "编译器戳记无法解析").WithDetail(err.Error())
	}
	if st.CompilerSHA256 != compilerSHA {
		return staleCompiler("编译器二进制与戳记不符")
	}
	for rel, want := range st.Sources {
		var got string
		var err error
		if rel == "sdk/cmd/compile/internal/gc/main.go" {
			got, err = hashFile(filepath.Join(sdkDir, "src", "cmd", "compile", "internal", "gc", "main.go"))
		} else {
			got, err = hashFile(filepath.Join(root, filepath.FromSlash(rel)))
		}
		if err != nil {
			return staleCompiler(fmt.Sprintf("无法读取 %s", rel))
		}
		if got != want {
			return staleCompiler(fmt.Sprintf("%s 已变化", rel))
		}
	}
	return nil
}

func staleCompiler(reason string) error {
	return diagnostic.New(diagnostic.Environment, "build.stale-compiler", "修改版编译器已过期（%s）；重新运行 ./bridge/go/build", reason)
}

func writeRecord(path string, record *Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "build.record-failed", "无法序列化构建记录").WithDetail(err.Error())
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return diagnostic.New(diagnostic.Environment, "build.record-failed", "无法写入构建记录 %s", path).WithDetail(err.Error())
	}
	return nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
