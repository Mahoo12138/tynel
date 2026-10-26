// Package sourcetest 是源码到原生的回归集合。
//
// 它从 tests/source/testdata/cases.json 读取用例，用真实前端宿主、降低器和
// 后端把每个程序编成原生二进制，再逐字节比较输出。参考结果有两个来源：
// 清单里的 stdout 是提交进仓库的黄金输出，reference.json 锁定用于对照的
// Node 配置；有 Node 的机器上会把两者再对一遍。
//
// 依赖 01.01 的工具链、02.01 的前端源码和 01.05 的修改版编译器。缺任何一个
// 都直接失败而不是跳过——跳过会让「零用例」看起来像通过。
package sourcetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
	"github.com/Mahoo12138/tynel/compiler/frontend"
	"github.com/Mahoo12138/tynel/compiler/nir"
	"github.com/Mahoo12138/tynel/compiler/pipeline"
	"github.com/Mahoo12138/tynel/internal/build"
	"github.com/Mahoo12138/tynel/internal/host"
	"github.com/Mahoo12138/tynel/internal/toolchain"
)

const (
	manifestPath  = "tests/source/testdata/cases.json"
	referencePath = "tests/source/reference.json"
	testdataDir   = "tests/source/testdata"
	runTimeout    = 30 * time.Second
)

type manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Cases         []caseSpec `json:"cases"`
}

type caseSpec struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	Kind   string `json:"kind"`
	Stdout string `json:"stdout"`
	Code   string `json:"code"`
	Note   string `json:"note"`
}

type reference struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Runtime       string                   `json:"runtime"`
	Version       string                   `json:"version"`
	Major         int                      `json:"major"`
	Command       string                   `json:"command"`
	CapturedAt    string                   `json:"capturedAt"`
	Note          string                   `json:"note"`
	Cases         map[string]referenceCase `json:"cases"`
}

type referenceCase struct {
	Comparison string `json:"comparison"`
	Note       string `json:"note"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("无法定位仓库根：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(toolchain.LockPath))); err != nil {
		t.Fatalf("仓库根 %s 缺少锁文件：%v", root, err)
	}
	return root
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		t.Fatalf("%s 无法解析：%v", path, err)
	}
}

func loadManifest(t *testing.T, root string) *manifest {
	t.Helper()
	var m manifest
	loadJSON(t, filepath.Join(root, filepath.FromSlash(manifestPath)), &m)
	if m.SchemaVersion != 1 {
		t.Fatalf("清单版本为 %d，本版本只接受 1", m.SchemaVersion)
	}
	return &m
}

func loadReference(t *testing.T, root string) *reference {
	t.Helper()
	var ref reference
	loadJSON(t, filepath.Join(root, filepath.FromSlash(referencePath)), &ref)
	if ref.SchemaVersion != 1 {
		t.Fatalf("对照配置版本为 %d，本版本只接受 1", ref.SchemaVersion)
	}
	return &ref
}

// ensureHost 准备前端宿主。宿主首次使用时会用隔离工具链构建。
func ensureHost(t *testing.T, root string) *host.Host {
	t.Helper()
	h, err := host.Ensure(root, toolchain.Exec{}, nil)
	if err != nil {
		t.Fatalf("前端宿主不可用：%v\n先运行 ./tools/toolchain/prep --frontend-only 与 ./tools/build", err)
	}
	return h
}

func caseFile(root string, spec caseSpec) string {
	return filepath.Join(root, filepath.FromSlash(testdataDir), filepath.FromSlash(spec.File))
}

// buildCase 走完整流程：前端、能力、降低、NIR 编码、后端。返回记录与前端结果。
func buildCase(t *testing.T, root string, h *host.Host, spec caseSpec, outDir string) (*build.Record, *pipeline.Result) {
	t.Helper()
	file := caseFile(root, spec)
	result, err := h.Analyze(file, "build", toolchain.Exec{})
	if err != nil {
		t.Fatalf("%s：前端失败：%v", spec.Name, err)
	}
	if code := result.ExitCode(); code != 0 {
		t.Fatalf("%s：应能编译，实际退出状态 %d：%+v %+v", spec.Name, code, result.Diagnostics, result.Capability)
	}
	if result.NIR == nil {
		t.Fatalf("%s：前端没有返回 NIR", spec.Name)
	}
	data, err := nir.EncodeBytes(result.NIR)
	if err != nil {
		t.Fatalf("%s：NIR 无法编码：%v", spec.Name, err)
	}
	record, err := build.Run(build.Options{
		Root:      root,
		InputData: data,
		Output:    filepath.Join(outDir, spec.Name),
		Source:    &build.Source{Path: spec.File, SHA256: result.SourceSHA256, Bytes: result.SourceBytes},
	}, toolchain.Exec{})
	if err != nil {
		t.Fatalf("%s：构建失败：%v", spec.Name, err)
	}
	return record, result
}

// runArtifact 运行产物并返回标准输出与退出状态。超时按失败处理，不挂住测试。
func runArtifact(t *testing.T, path string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("程序 %s 超过 %s 未结束", path, runTimeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("运行 %s 失败：%v", path, err)
		}
		return stdout.String(), exitErr.ExitCode()
	}
	return stdout.String(), 0
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestManifest 确认清单本身可用：版本、数量、命名、种类和文件都存在。
// 用例为零时直接失败，避免「零测试」被当成通过。
func TestManifest(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	if len(m.Cases) == 0 {
		t.Fatal("用例清单为空；没有用例不算通过")
	}
	seen := make(map[string]bool, len(m.Cases))
	kinds := map[string]int{}
	for _, spec := range m.Cases {
		if spec.Name == "" || spec.File == "" {
			t.Fatalf("用例缺少名字或文件：%+v", spec)
		}
		if seen[spec.Name] {
			t.Fatalf("用例名 %q 重复", spec.Name)
		}
		seen[spec.Name] = true
		kinds[spec.Kind]++
		switch spec.Kind {
		case "program":
			if spec.Stdout == "" {
				t.Errorf("%s：程序用例必须给出完整输出", spec.Name)
			}
		case "source-error", "capability":
			if spec.Code == "" {
				t.Errorf("%s：错误用例必须给出错误码", spec.Name)
			}
		case "environment":
			if _, err := os.Stat(caseFile(root, spec)); !os.IsNotExist(err) {
				t.Errorf("%s：环境用例的文件不应存在", spec.Name)
			}
			continue
		default:
			t.Errorf("%s：未知的用例种类 %q", spec.Name, spec.Kind)
		}
		if _, err := os.Stat(caseFile(root, spec)); err != nil {
			t.Errorf("%s：源文件不可读：%v", spec.Name, err)
		}
	}
	for _, kind := range []string{"program", "source-error", "capability", "environment"} {
		if kinds[kind] == 0 {
			t.Errorf("缺少 %s 类用例", kind)
		}
	}
}

// TestPrograms 编译并运行每个程序用例，逐字节比较输出。
//
// 比较的是完整标准输出，不做任何行过滤：删掉输出行去迎合参考结果，在这里
// 会直接失败。
func TestPrograms(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	h := ensureHost(t, root)
	outDir := t.TempDir()

	ran := 0
	for _, spec := range m.Cases {
		if spec.Kind != "program" {
			continue
		}
		ran++
		t.Run(spec.Name, func(t *testing.T) {
			record, _ := buildCase(t, root, h, spec, outDir)
			if record.Output.Bytes == 0 {
				t.Fatalf("%s：产物为空", spec.Name)
			}
			got, code := runArtifact(t, record.Output.Path)
			if code != 0 {
				t.Fatalf("%s：程序退出状态为 %d", spec.Name, code)
			}
			if got != spec.Stdout {
				t.Fatalf("%s：输出为 %q，参考为 %q", spec.Name, got, spec.Stdout)
			}
		})
	}
	if ran == 0 {
		t.Fatal("没有执行任何程序用例")
	}
}

// TestErrors 检查源码错误、能力限制与环境缺失的退出状态和错误码。
func TestErrors(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	h := ensureHost(t, root)

	ran := 0
	for _, spec := range m.Cases {
		if spec.Kind == "program" {
			continue
		}
		ran++
		t.Run(spec.Name, func(t *testing.T) {
			result, err := h.Analyze(caseFile(root, spec), "check", toolchain.Exec{})
			if spec.Kind == "environment" {
				if err == nil {
					t.Fatalf("%s：应报环境缺失，实际返回结果", spec.Name)
				}
				if code := exitCode(err); code != 4 {
					t.Fatalf("%s：退出状态为 %d，期望 4（%v）", spec.Name, code, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s：前端失败：%v", spec.Name, err)
			}
			want := 1
			if spec.Kind == "capability" {
				want = 3
			}
			if code := result.ExitCode(); code != want {
				t.Fatalf("%s：退出状态为 %d，期望 %d", spec.Name, code, want)
			}
			if spec.Kind == "source-error" {
				for _, d := range result.Diagnostics {
					if strconv.Itoa(int(d.Code)) == spec.Code {
						return
					}
				}
				t.Fatalf("%s：诊断里没有错误码 %s：%+v", spec.Name, spec.Code, result.Diagnostics)
			}
			for _, f := range result.Capability {
				if f.Code == spec.Code {
					return
				}
			}
			t.Fatalf("%s：能力项里没有 %s：%+v", spec.Name, spec.Code, result.Capability)
		})
	}
	if ran == 0 {
		t.Fatal("没有执行任何错误用例")
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	return diagnostic.ExitCode(err)
}

// TestIdentityChain 确认源码、NIR、产物三者的哈希互相对应，并且用记录里的
// 快照回放能得到逐字节相同的产物。
func TestIdentityChain(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	h := ensureHost(t, root)
	outDir := t.TempDir()

	var add *caseSpec
	for i := range m.Cases {
		if m.Cases[i].Name == "add" {
			add = &m.Cases[i]
		}
	}
	if add == nil {
		t.Fatal("清单里没有 add 用例")
	}
	record, result := buildCase(t, root, h, *add, outDir)

	if want := hashFile(t, caseFile(root, *add)); record.Source.SHA256 != want {
		t.Errorf("记录里的源码哈希与文件不符：%s != %s", record.Source.SHA256, want)
	}
	if record.Source.SHA256 != result.SourceSHA256 {
		t.Errorf("记录与前端结果里的源码哈希不一致")
	}
	if want := hashFile(t, record.Tools.Snapshot); record.Input.SHA256 != want {
		t.Errorf("记录里的 NIR 哈希与快照不符：%s != %s", record.Input.SHA256, want)
	}
	if want := hashFile(t, record.Output.Path); record.Output.SHA256 != want {
		t.Errorf("记录里的产物哈希与文件不符：%s != %s", record.Output.SHA256, want)
	}

	replay := filepath.Join(outDir, "replay")
	if _, err := build.Run(build.Options{Root: root, Input: record.Tools.Snapshot, Output: replay}, toolchain.Exec{}); err != nil {
		t.Fatalf("用快照回放失败：%v", err)
	}
	if a, b := hashFile(t, record.Output.Path), hashFile(t, replay); a != b {
		t.Fatalf("回放产物与首次构建不同：%s != %s", a, b)
	}
	if got, _ := runArtifact(t, replay); got != add.Stdout {
		t.Fatalf("回放产物输出 %q，参考 %q", got, add.Stdout)
	}
}

// TestSerialization 分别验证两条序列化路径：NIR 编码往返与事实解码往返。
// 算术由 TestPrograms 负责，这里只看数据形状。
func TestSerialization(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	h := ensureHost(t, root)

	var add *caseSpec
	for i := range m.Cases {
		if m.Cases[i].Name == "add" {
			add = &m.Cases[i]
		}
	}
	if add == nil {
		t.Fatal("清单里没有 add 用例")
	}

	result, err := h.Analyze(caseFile(root, *add), "facts", toolchain.Exec{})
	if err != nil {
		t.Fatalf("前端失败：%v", err)
	}
	if result.Facts == nil {
		t.Fatal("没有拿到事实")
	}
	factsJSON, err := json.Marshal(result.Facts)
	if err != nil {
		t.Fatalf("事实无法编码：%v", err)
	}
	decoded, err := frontend.Decode(factsJSON)
	if err != nil {
		t.Fatalf("事实无法解码：%v", err)
	}
	if !reflect.DeepEqual(decoded, result.Facts) {
		t.Fatal("事实往返后结构不一致")
	}

	buildResult, err := h.Analyze(caseFile(root, *add), "nir", toolchain.Exec{})
	if err != nil {
		t.Fatalf("前端失败：%v", err)
	}
	first, err := nir.EncodeBytes(buildResult.NIR)
	if err != nil {
		t.Fatalf("NIR 无法编码：%v", err)
	}
	module, err := nir.DecodeBytes(first, nir.DefaultLimits())
	if err != nil {
		t.Fatalf("NIR 无法解码：%v", err)
	}
	second, err := nir.EncodeBytes(module)
	if err != nil {
		t.Fatalf("NIR 无法再次编码：%v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("NIR 往返后字节不一致")
	}
}

// TestBuildFailureKeepsArtifact 确认构建失败不会动已有的产物和记录。
func TestBuildFailureKeepsArtifact(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	h := ensureHost(t, root)
	outDir := t.TempDir()

	var add *caseSpec
	for i := range m.Cases {
		if m.Cases[i].Name == "add" {
			add = &m.Cases[i]
		}
	}
	if add == nil {
		t.Fatal("清单里没有 add 用例")
	}
	record, _ := buildCase(t, root, h, *add, outDir)
	artifactBefore := hashFile(t, record.Output.Path)
	recordBefore := hashFile(t, build.RecordPath(record.Output.Path))

	// 入口指向不存在的函数：解码成功、验证失败，构建必须在发布之前停下。
	bad := []byte(`{"schemaVersion":1,"entry":"nope","functions":[{"name":"main","params":[],"result":"void","body":[{"op":"return"}]}]}`)
	_, err := build.Run(build.Options{Root: root, InputData: bad, Output: record.Output.Path}, toolchain.Exec{})
	if err == nil {
		t.Fatal("坏 NIR 应被拒绝")
	}
	if got := hashFile(t, record.Output.Path); got != artifactBefore {
		t.Fatalf("失败时改动了已有产物：%s != %s", got, artifactBefore)
	}
	if got := hashFile(t, build.RecordPath(record.Output.Path)); got != recordBefore {
		t.Fatalf("失败时改动了构建记录")
	}
}

// TestNodeReference 把清单里的参考输出与 Node 的实际输出对一遍。
//
// 没有 Node 时跳过并说明原因：跳过不是通过，主回归仍由 TestPrograms 完成。
func TestNodeReference(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	ref := loadReference(t, root)

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("本机没有 node；参考对照只在有 Node 的机器上执行")
	}
	versionOut, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Fatalf("无法读取 node 版本：%v", err)
	}
	version := strings.TrimSpace(string(versionOut))
	major := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 2)[0]
	if major != strconv.Itoa(ref.Major) {
		t.Skipf("Node 主版本为 %s，参考锁定 %d；跳过对照", major, ref.Major)
	}

	ran := 0
	for _, spec := range m.Cases {
		if spec.Kind != "program" {
			continue
		}
		entry, ok := ref.Cases[spec.Name]
		if !ok {
			t.Fatalf("%s：reference.json 缺少对照配置", spec.Name)
		}
		t.Run(spec.Name, func(t *testing.T) {
			ran++
			got := runNode(t, node, caseFile(root, spec))
			switch entry.Comparison {
			case "shared":
				if got != spec.Stdout {
					t.Fatalf("%s：Node 输出 %q，清单参考 %q", spec.Name, got, spec.Stdout)
				}
			case "spelling":
				if normalizeSpelling(spec.Stdout) != got {
					t.Fatalf("%s：归一拼写后仍与 Node 不同：%q != %q", spec.Name, normalizeSpelling(spec.Stdout), got)
				}
			default:
				t.Fatalf("%s：未知的对照方式 %q", spec.Name, entry.Comparison)
			}
		})
	}
	if ran == 0 {
		t.Fatal("没有执行任何对照用例")
	}
}

func runNode(t *testing.T, node, file string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--experimental-strip-types", file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node 运行 %s 失败：%v\n%s", file, err, stderr.String())
	}
	return stdout.String()
}

// normalizeSpelling 把 Tynel 的特殊值拼写换成 JavaScript 的写法。
func normalizeSpelling(out string) string {
	out = strings.ReplaceAll(out, "-Inf", "-Infinity")
	out = strings.ReplaceAll(out, "+Inf", "Infinity")
	return out
}
