// Package host 管理 TypeScript 前端宿主进程。
//
// 主命令行不能导入 tsgo：前端在另一个模块里，仓库根模块不依赖它。所以前端以
// 子进程方式使用，本包负责三件事：确认宿主二进制与当前源码一致、必要时用隔离
// 工具链重建它、把一次检查的结果取回来。
//
// 宿主二进制带一份戳记，记录它自己与全部源码文件的哈希。源码变了而二进制没
// 重建时，命令行会先重建再运行——用旧宿主检查新代码，结论不可信。
package host

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

// 仓库内的固定位置。
const (
	ConsumerDir = "bridge/tsgo/consumer"
	AdapterDir  = "bridge/tsgo/_adapter"
	TreeDir     = ".work/frontend/typescript-go"
	BinaryPath  = ".work/frontend/bin/consumer"
	StampPath   = ".work/frontend/bin/consumer.stamp.json"
)

// Host 是一个已经就绪的前端宿主。
type Host struct {
	Path string
}

type stamp struct {
	SchemaVersion int               `json:"schemaVersion"`
	BinarySHA256  string            `json:"binarySha256"`
	BuiltAt       string            `json:"builtAt"`
	Sources       map[string]string `json:"sources"`
}

// Ensure 返回可用的前端宿主。二进制缺失、过期或与源码不一致时先重建。
//
// progress 接收进度行（通常是命令行的 stderr）；结果本身永远不写进去。
func Ensure(root string, runner toolchain.Runner, progress io.Writer) (*Host, error) {
	tree := filepath.Join(root, filepath.FromSlash(TreeDir))
	if info, err := os.Stat(tree); err != nil || !info.IsDir() {
		return nil, diagnostic.New(diagnostic.Environment, "host.no-tree",
			"找不到前端源码 %s；先运行 ./tools/toolchain/prep --frontend-only", TreeDir)
	}
	if err := verifyAdapter(tree, filepath.Join(root, filepath.FromSlash(AdapterDir))); err != nil {
		return nil, err
	}

	sources, err := hashSources(root)
	if err != nil {
		return nil, err
	}
	binary := filepath.Join(root, filepath.FromSlash(BinaryPath))
	if ok, err := stampMatches(binary, filepath.Join(root, filepath.FromSlash(StampPath)), sources); err == nil && ok {
		return &Host{Path: binary}, nil
	}

	if progress != nil {
		fmt.Fprintf(progress, "前端宿主需要重建（源码有变化或尚未构建）\n")
	}
	if err := build(root, binary, runner); err != nil {
		return nil, err
	}
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() {
		return nil, diagnostic.New(diagnostic.Internal, "host.no-binary", "构建报告成功但没有产出 %s", BinaryPath)
	}
	sum, err := hashFile(binary)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "host.binary-unreadable", "无法读取前端宿主").WithDetail(err.Error())
	}
	if err := writeStamp(filepath.Join(root, filepath.FromSlash(StampPath)), &stamp{
		SchemaVersion: 1,
		BinarySHA256:  sum,
		BuiltAt:       time.Now().Format(time.RFC3339),
		Sources:       sources,
	}); err != nil {
		return nil, err
	}
	return &Host{Path: binary}, nil
}

// Analyze 让宿主检查一个文件，返回协议结果。want 决定附带事实还是 NIR。
func (h *Host) Analyze(file, want string, runner toolchain.Runner) (*pipeline.Result, error) {
	out, err := runner.Run(h.Path, "-pipeline", "-want="+want, file)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "host.start-failed", "无法启动前端宿主 %s", h.Path).WithDetail(err.Error())
	}
	if out.ExitCode != 0 {
		category := diagnostic.Environment
		if out.ExitCode == diagnostic.ExitInternal {
			category = diagnostic.Internal
		}
		d := diagnostic.New(category, "host.failed", "前端宿主执行失败（退出状态 %d）", out.ExitCode)
		if detail := strings.TrimSpace(out.Stderr); detail != "" {
			d = d.WithDetail(detail)
		}
		return nil, d
	}
	result, err := pipeline.Decode([]byte(out.Stdout))
	if err != nil {
		return nil, diagnostic.New(diagnostic.Internal, "host.bad-result", "前端宿主返回的结果无法解析").WithDetail(err.Error())
	}
	return result, nil
}

// build 用隔离工具链编译宿主。模块缓存留在 .work 内，编译缓存用默认位置。
func build(root, binary string, runner toolchain.Runner) error {
	report, err := toolchain.LoadReport(filepath.Join(root, filepath.FromSlash(toolchain.ReportPath)))
	if err != nil {
		return err
	}
	sdk, ok := report.SDK()
	if !ok {
		return diagnostic.New(diagnostic.Environment, "host.no-sdk", "准备报告里没有 SDK 记录；重新运行 ./tools/toolchain/prep")
	}
	goTool := filepath.Join(sdk.Dir, "bin", "go")
	proxy := os.Getenv("TYNEL_GOPROXY")
	if proxy == "" {
		proxy = "https://goproxy.cn"
	}
	env := append(os.Environ(),
		"GOMODCACHE="+filepath.Join(root, filepath.FromSlash(".work/frontend/gomodcache")),
		"GOPROXY="+proxy,
		"GOSUMDB=off",
	)
	args := []string{"-C", filepath.Join(root, filepath.FromSlash(ConsumerDir)), "build", "-o", binary, "."}
	out, err := runner.RunEnv(env, goTool, args...)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "host.build-failed", "无法启动 %s", goTool).WithDetail(err.Error())
	}
	if out.ExitCode != 0 {
		d := diagnostic.New(diagnostic.Environment, "host.build-failed", "前端宿主构建失败（退出状态 %d）", out.ExitCode)
		if detail := strings.TrimSpace(out.Stderr); detail != "" {
			d = d.WithDetail(detail)
		}
		return d
	}
	return nil
}

// hashSources 汇总宿主编译真正用到的源码：消费者模块的 .go 与 go.mod/go.sum，
// 以及适配包源码。键是仓库内相对路径，值是该文件的 sha256。
func hashSources(root string) (map[string]string, error) {
	out := make(map[string]string)
	for _, dir := range []string{ConsumerDir, AdapterDir} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		entries, err := os.ReadDir(full)
		if err != nil {
			return nil, diagnostic.New(diagnostic.Environment, "host.no-source", "无法读取 %s", dir).WithDetail(err.Error())
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, "_test.go") {
				// 测试文件不参与宿主编译，改它们不该触发重建。
				continue
			}
			if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
				continue
			}
			sum, err := hashFile(filepath.Join(full, name))
			if err != nil {
				return nil, diagnostic.New(diagnostic.Environment, "host.no-source", "无法读取 %s/%s", dir, name).WithDetail(err.Error())
			}
			out[dir+"/"+name] = sum
		}
	}
	return out, nil
}

// verifyAdapter 确认源码树里的适配包副本与仓库源一致。副本被改过时不能继续：
// 宿主会编译那份副本，结论就不再对应仓库里的代码。
func verifyAdapter(tree, adapter string) error {
	entries, err := os.ReadDir(adapter)
	if err != nil {
		return diagnostic.New(diagnostic.Environment, "host.no-adapter", "无法读取适配包源码 %s", AdapterDir).WithDetail(err.Error())
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		count++
		want, err := os.ReadFile(filepath.Join(adapter, e.Name()))
		if err != nil {
			return diagnostic.New(diagnostic.Environment, "host.no-adapter", "无法读取 %s/%s", AdapterDir, e.Name()).WithDetail(err.Error())
		}
		got, err := os.ReadFile(filepath.Join(tree, "tyneladapter", e.Name()))
		if err != nil {
			return adapterStale(e.Name())
		}
		if string(want) != string(got) {
			return adapterStale(e.Name())
		}
	}
	if count == 0 {
		return diagnostic.New(diagnostic.Environment, "host.no-adapter", "适配包源码目录 %s 里没有 .go 文件", AdapterDir)
	}
	return nil
}

func adapterStale(name string) error {
	return diagnostic.New(diagnostic.Environment, "host.stale-adapter",
		"源码树里的适配包副本与 %s 不一致（%s）；运行 ./tools/toolchain/prep --frontend-only 重新同步", AdapterDir, name)
}

func stampMatches(binary, stampPath string, sources map[string]string) (bool, error) {
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() {
		return false, err
	}
	data, err := os.ReadFile(stampPath)
	if err != nil {
		return false, err
	}
	var st stamp
	if err := json.Unmarshal(data, &st); err != nil {
		return false, err
	}
	if st.SchemaVersion != 1 || len(st.Sources) != len(sources) {
		return false, nil
	}
	for k, v := range sources {
		if st.Sources[k] != v {
			return false, nil
		}
	}
	sum, err := hashFile(binary)
	if err != nil {
		return false, err
	}
	return st.BinarySHA256 == sum, nil
}

func writeStamp(path string, st *stamp) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return diagnostic.New(diagnostic.Internal, "host.stamp-failed", "无法序列化宿主戳记").WithDetail(err.Error())
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return diagnostic.New(diagnostic.Environment, "host.stamp-failed", "无法创建目录 %s", filepath.Dir(path)).WithDetail(err.Error())
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return diagnostic.New(diagnostic.Environment, "host.stamp-failed", "无法写入宿主戳记").WithDetail(err.Error())
	}
	return nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// SortedSourceKeys 只给测试与诊断用：把源码键按字典序排好，方便比较。
func SortedSourceKeys(sources map[string]string) []string {
	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
