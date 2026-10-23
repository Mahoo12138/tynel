// Command prep 按 tools/toolchain/go.lock.json 在项目内的 .work/ 目录准备隔离
// Go 工具链与 TypeScript 前端源码。
//
//	go run tools/toolchain/prep.go                 # 准备 SDK、源码与前端；重复执行只做校验
//	go run tools/toolchain/prep.go --force         # 丢弃缓存和半成品目录后重新准备
//	go run tools/toolchain/prep.go --check-remote  # 额外复核 Go 标签与前端提交
//	go run tools/toolchain/prep.go --verify-only   # 只校验已解压的前端源码
//
// 命令只写项目内的 .work/，不修改宿主 Go 安装，也不写入全局环境变量或代理配置。
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// 退出码区分失败类别，便于脚本判断和负向验收。
const (
	exitInternal = 1 // 未分类的内部故障
	exitUsage    = 2 // 参数用法错误
	exitLock     = 3 // 锁文件缺失、格式错误或内容非法
	exitHost     = 4 // 宿主平台与锁文件目标不一致
	exitNet      = 5 // 下载或网络失败
	exitHash     = 6 // 校验和不符
	exitFS       = 7 // 目录、权限或解压失败
	exitVerify   = 8 // 版本、工具或完整性校验失败
)

const (
	lockRelativePath = "tools/toolchain/go.lock.json"
	workDirName      = ".work"
	downloadDirName  = "downloads"
	toolchainDirName = "toolchain"
	manifestDirName  = "manifests"
	reportName       = "toolchain.json"
	frontendDirName  = "frontend"
)

// artifact 是锁文件中的一份可下载文件及其身份。
type artifact struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type lockGo struct {
	Version       string   `json:"version"`
	SourceTag     string   `json:"sourceTag"`
	SourceCommit  string   `json:"sourceCommit"`
	License       string   `json:"license"`
	LicenseFile   string   `json:"licenseFile"`
	SourceArchive artifact `json:"sourceArchive"`
	SDKArchive    artifact `json:"sdkArchive"`
}

type lockTargets struct {
	Host     string   `json:"host"`
	Primary  string   `json:"primary"`
	Deferred []string `json:"deferred"`
}

type lockBootstrap struct {
	MinimumGoVersion string `json:"minimumGoVersion"`
	Derivation       string `json:"derivation"`
	HostGoObserved   string `json:"hostGoObserved"`
	Note             string `json:"note"`
}

type patchFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type lockBridge struct {
	PatchFiles []patchFile `json:"patchFiles"`
}

// markerFile 是源码树里必须逐字节匹配的文件，用于判定解压出的内容是否就是
// 锁文件登记的那一份。
type markerFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// lockAdapter 说明适配包源码在仓库里的位置，以及它在上游源码树里的包名。
type lockAdapter struct {
	Source  string `json:"source"`
	Package string `json:"package"`
}

// lockFrontend 固定 TypeScript 前端源码的身份。上游模块的包全部是 internal，
// 模块外的代码无法导入，所以适配包必须复制进这棵树才能编译。
type lockFrontend struct {
	Name        string       `json:"name"`
	Repository  string       `json:"repository"`
	Module      string       `json:"module"`
	Version     string       `json:"version"`
	Commit      string       `json:"commit"`
	License     string       `json:"license"`
	LicenseFile string       `json:"licenseFile"`
	GoDirective string       `json:"goDirective"`
	Archive     artifact     `json:"archive"`
	ArchiveRoot string       `json:"archiveRoot"`
	Markers     []markerFile `json:"markers"`
	Adapter     lockAdapter  `json:"adapter"`
}

type lockFile struct {
	SchemaVersion int           `json:"schemaVersion"`
	LockedAt      string        `json:"lockedAt"`
	Go            lockGo        `json:"go"`
	Targets       lockTargets   `json:"targets"`
	Bootstrap     lockBootstrap `json:"bootstrap"`
	Bridge        lockBridge    `json:"bridge"`
	Frontend      lockFrontend  `json:"frontend"`
}

// manifest 记录一次成功安装的来源身份，用于下次运行时判定目录是否完整。
type manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Version       string            `json:"version"`
	VersionStamp  string            `json:"versionStamp,omitempty"`
	Target        string            `json:"target"`
	Archive       artifact          `json:"archive"`
	InstalledAt   string            `json:"installedAt"`
	Tools         map[string]string `json:"tools,omitempty"`
}

// install 是一次准备动作的结果，也用于写入总报告。
type install struct {
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Dir      string            `json:"dir"`
	Version  string            `json:"version"`
	Stamp    string            `json:"versionStamp,omitempty"`
	Target   string            `json:"target"`
	Archive  artifact          `json:"archive"`
	Tools    map[string]string `json:"tools,omitempty"`
	Reused   bool              `json:"reused"`
	Verified bool              `json:"verified"`
}

type hostInfo struct {
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
	GoBinary        string `json:"goBinary"`
	GoVersion       string `json:"goVersion"`
	GOROOT          string `json:"goroot"`
	GOROOTUnchanged bool   `json:"gorootUnchanged"`
}

type report struct {
	SchemaVersion int       `json:"schemaVersion"`
	PreparedAt    string    `json:"preparedAt"`
	ProjectRoot   string    `json:"projectRoot"`
	LockFile      string    `json:"lockFile"`
	LockedVersion string    `json:"lockedVersion"`
	SourceTag     string    `json:"sourceTag"`
	SourceCommit  string    `json:"sourceCommit"`
	License       string    `json:"license"`
	Target        string    `json:"target"`
	Host          hostInfo  `json:"host"`
	Installs      []install `json:"installs"`
	Frontend      *install  `json:"frontend,omitempty"`
	PatchFiles    []string  `json:"patchFiles"`
	Notes         []string  `json:"notes"`
}

// failErr 把退出码和可读原因绑定在一起。
type failErr struct {
	code int
	err  error
}

func (e *failErr) Error() string { return e.err.Error() }
func (e *failErr) Unwrap() error { return e.err }

func failf(code int, format string, args ...any) error {
	return &failErr{code: code, err: fmt.Errorf(format, args...)}
}

func main() {
	if err := run(); err != nil {
		code := exitInternal
		var fe *failErr
		if errors.As(err, &fe) {
			code = fe.code
		}
		fmt.Fprintf(os.Stderr, "准备失败：%v\n", err)
		os.Exit(code)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法：go run tools/toolchain/prep.go [选项]

按 tools/toolchain/go.lock.json 在 .work/ 下准备隔离 Go 工具链与前端源码，只写项目目录。

选项：
  --project DIR    指定项目根目录，默认按本文件位置推断
  --force          删除缓存的下载和半成品目录后重新准备
  --check-remote   复核 Go 源码标签与前端提交是否仍是锁文件登记的那个
  --frontend-only  只准备 TypeScript 前端源码，跳过 Go 工具链
  --verify-only    只校验已解压的前端源码与适配包副本，不下载、不写入（隐含 --frontend-only）
  --quiet          只输出错误和报告路径
`)
}

func run() error {
	fs := flag.NewFlagSet("prep", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "项目根目录，默认按本文件位置推断")
	force := fs.Bool("force", false, "删除缓存的下载和半成品目录后重新准备")
	checkRemote := fs.Bool("check-remote", false, "复核 Go 源码标签与前端提交")
	frontendOnly := fs.Bool("frontend-only", false, "只准备 TypeScript 前端源码")
	verifyOnly := fs.Bool("verify-only", false, "只校验已解压的前端源码，不下载、不写入")
	quiet := fs.Bool("quiet", false, "只输出错误和报告路径")
	fs.SetOutput(os.Stderr)
	fs.Usage = usage
	if err := fs.Parse(os.Args[1:]); err != nil {
		return failf(exitUsage, "%v", err)
	}
	if fs.NArg() > 0 {
		return failf(exitUsage, "不接受位置参数：%s", strings.Join(fs.Args(), " "))
	}
	if *verifyOnly {
		// 校验模式只读：它不解压、不写清单，所以也只能针对前端源码。
		*frontendOnly = true
	}

	root, err := resolveRoot(*projectFlag)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(root, filepath.FromSlash(lockRelativePath))
	lock, err := loadLock(lockPath)
	if err != nil {
		return err
	}
	host, err := inspectHost()
	if err != nil {
		return err
	}
	if want := host.GOOS + "/" + host.GOARCH; lock.Targets.Host != want {
		return failf(exitHost, "锁文件目标为 %s，本机是 %s；只能准备与本机匹配的 SDK。", lock.Targets.Host, want)
	}

	workDir := filepath.Join(root, workDirName)
	downloadDir := filepath.Join(workDir, downloadDirName)
	toolchainDir := filepath.Join(workDir, toolchainDirName)
	manifestDir := filepath.Join(toolchainDir, manifestDirName)
	frontendDir := filepath.Join(workDir, frontendDirName)
	dirs := []string{downloadDir, manifestDir}
	if !*frontendOnly {
		dirs = append(dirs, toolchainDir)
	}
	if !*verifyOnly {
		dirs = append(dirs, frontendDir)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return failf(exitFS, "无法创建目录 %s：%v", dir, err)
		}
	}

	patchPaths, err := verifyPatchFiles(root, lock)
	if err != nil {
		return err
	}
	if len(patchPaths) > 0 && !*quiet {
		fmt.Printf("接入文件：%d 个，校验和与锁文件一致\n", len(patchPaths))
	}

	if *checkRemote {
		if !*frontendOnly {
			if err := checkRemoteTag(lock); err != nil {
				return err
			}
			if !*quiet {
				fmt.Printf("远程复核：标签 %s 对应提交 %s，与锁文件一致\n", lock.Go.SourceTag, shortSHA(lock.Go.SourceCommit))
			}
		}
		if err := checkRemoteFrontend(lock); err != nil {
			return err
		}
		if !*quiet {
			fmt.Printf("远程复核：前端 %s 对应提交 %s，与锁文件一致\n", lock.Frontend.Version, shortSHA(lock.Frontend.Commit))
		}
	}

	installs := make([]install, 0, 2)
	if !*frontendOnly {
		targetSlug := strings.ReplaceAll(lock.Targets.Host, "/", "-")
		sdk, err := prepareOne("sdk", lock.Go.SDKArchive, lock.Go.Version+"-sdk-"+targetSlug, lock, downloadDir, toolchainDir, manifestDir, *force, *quiet)
		if err != nil {
			return err
		}
		src, err := prepareOne("src", lock.Go.SourceArchive, lock.Go.Version+"-src", lock, downloadDir, toolchainDir, manifestDir, *force, *quiet)
		if err != nil {
			return err
		}
		installs = append(installs, *sdk, *src)

		after, err := inspectHost()
		if err != nil {
			return err
		}
		host.GOROOTUnchanged = after.GOROOT == host.GOROOT && after.GoBinary == host.GoBinary
		if !host.GOROOTUnchanged {
			return failf(exitVerify, "宿主 Go 安装在准备过程中发生变化：GOROOT %q → %q", host.GOROOT, after.GOROOT)
		}
	}

	frontend, err := prepareFrontend(root, lock, downloadDir, frontendDir, manifestDir, *force, *verifyOnly, *quiet)
	if err != nil {
		return err
	}
	if *verifyOnly {
		if !*quiet {
			fmt.Printf("前端源码校验通过：%s（%s）\n", frontend.Dir, lock.Frontend.Version)
		}
		return nil
	}

	rep := &report{
		SchemaVersion: 1,
		PreparedAt:    time.Now().Format(time.RFC3339),
		ProjectRoot:   root,
		LockFile:      lockPath,
		LockedVersion: lock.Go.Version,
		SourceTag:     lock.Go.SourceTag,
		SourceCommit:  lock.Go.SourceCommit,
		License:       lock.Go.License,
		Target:        lock.Targets.Host,
		Host:          host,
		Installs:      installs,
		Frontend:      frontend,
		PatchFiles:    patchPaths,
		Notes: []string{
			"工具链只写在项目 .work/ 内，宿主 Go 安装和全局代理设置未改变。",
			"使用隔离工具链需要显式设置 GOROOT 与 PATH，或在命令中直接使用绝对路径。",
			"源码目录尚未自举编译；修改版编译器的构建在 01.05 进行。",
			"前端源码只解压到 .work/frontend，适配包由 bridge/tsgo/adapter 复制进去，上游文件本身不修改。",
		},
	}
	reportPath := filepath.Join(toolchainDir, reportName)
	if *frontendOnly {
		// 只准备前端时不要覆盖工具链报告：那份报告描述的是 SDK 与源码的
		// 身份，被一份 installs 为空的记录顶掉之后，后续命令会找不到 go。
		reportPath = filepath.Join(frontendDir, "frontend.json")
	}
	if err := writeJSON(reportPath, rep); err != nil {
		return err
	}

	if !*quiet {
		printSummary(rep, reportPath, lock)
	} else {
		fmt.Println(reportPath)
	}
	return nil
}

func resolveRoot(projectFlag string) (string, error) {
	var root string
	if projectFlag != "" {
		abs, err := filepath.Abs(projectFlag)
		if err != nil {
			return "", failf(exitUsage, "无法解析 --project 路径 %q：%v", projectFlag, err)
		}
		root = abs
	} else {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			return "", failf(exitInternal, "无法定位准备入口所在目录，请用 --project 指定项目根目录")
		}
		if !filepath.IsAbs(file) {
			abs, err := filepath.Abs(file)
			if err != nil {
				return "", failf(exitInternal, "无法解析入口路径 %q：%v", file, err)
			}
			file = abs
		}
		root = filepath.Dir(filepath.Dir(filepath.Dir(file)))
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", failf(exitUsage, "项目根目录 %s 不可访问：%v", root, err)
	}
	if !info.IsDir() {
		return "", failf(exitUsage, "项目根 %s 不是目录", root)
	}
	return root, nil
}

var (
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	goVersionPat  = regexp.MustCompile(`^go[0-9]+\.[0-9]+(\.[0-9]+)?$`)
)

func loadLock(path string) (*lockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, failf(exitLock, "无法读取锁文件 %s：%v", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var lock lockFile
	if err := dec.Decode(&lock); err != nil {
		return nil, failf(exitLock, "锁文件 %s 不是预期的 JSON 结构：%v", path, err)
	}
	if err := validateLock(&lock); err != nil {
		return nil, failf(exitLock, "锁文件 %s 内容非法：%v", path, err)
	}
	return &lock, nil
}

func validateLock(lock *lockFile) error {
	if lock.SchemaVersion != 1 {
		return fmt.Errorf("不支持的 schemaVersion %d，本版本只认识 1", lock.SchemaVersion)
	}
	if !goVersionPat.MatchString(lock.Go.Version) {
		return fmt.Errorf("go.version %q 不是 goX.Y[.Z] 形式", lock.Go.Version)
	}
	if !goVersionPat.MatchString(lock.Go.SourceTag) {
		return fmt.Errorf("go.sourceTag %q 不是 goX.Y[.Z] 形式", lock.Go.SourceTag)
	}
	if len(lock.Go.SourceCommit) != 40 || !isHex(lock.Go.SourceCommit) {
		return fmt.Errorf("go.sourceCommit %q 不是 40 位十六进制提交", lock.Go.SourceCommit)
	}
	if lock.Go.License == "" || lock.Go.LicenseFile == "" {
		return fmt.Errorf("缺少 go.license 或 go.licenseFile")
	}
	for name, a := range map[string]artifact{"sourceArchive": lock.Go.SourceArchive, "sdkArchive": lock.Go.SDKArchive} {
		if err := validateArtifact(a); err != nil {
			return fmt.Errorf("%s：%v", name, err)
		}
	}
	if lock.Targets.Host == "" || !strings.Contains(lock.Targets.Host, "/") {
		return fmt.Errorf("targets.host %q 应为 GOOS/GOARCH 形式", lock.Targets.Host)
	}
	if lock.Targets.Host != lock.Targets.Primary {
		return fmt.Errorf("targets.host %q 与 targets.primary %q 不一致", lock.Targets.Host, lock.Targets.Primary)
	}
	for _, p := range lock.Bridge.PatchFiles {
		if p.Path == "" || !sha256Pattern.MatchString(p.SHA256) {
			return fmt.Errorf("bridge.patchFiles 中的条目 %q 缺少路径或合法 sha256", p.Path)
		}
	}
	if err := validateFrontend(lock.Frontend); err != nil {
		return fmt.Errorf("frontend：%v", err)
	}
	return nil
}

func validateFrontend(f lockFrontend) error {
	if f.Name == "" || f.Module == "" || f.Version == "" || f.Repository == "" {
		return fmt.Errorf("name、module、version、repository 都是必填项")
	}
	if len(f.Commit) != 40 || !isHex(f.Commit) {
		return fmt.Errorf("commit %q 不是 40 位十六进制提交", f.Commit)
	}
	if f.License == "" || f.LicenseFile == "" {
		return fmt.Errorf("license 与 licenseFile 都是必填项")
	}
	if !goVersionPat.MatchString("go" + f.GoDirective) {
		return fmt.Errorf("goDirective %q 不是 X.Y 形式", f.GoDirective)
	}
	if err := validateArtifact(f.Archive); err != nil {
		return fmt.Errorf("archive：%v", err)
	}
	if f.ArchiveRoot == "" || strings.ContainsAny(f.ArchiveRoot, `/\`) {
		return fmt.Errorf("archiveRoot %q 必须是单个目录名", f.ArchiveRoot)
	}
	if len(f.Markers) == 0 {
		return fmt.Errorf("markers 不能为空")
	}
	for _, m := range f.Markers {
		if m.Path == "" || strings.HasPrefix(m.Path, "/") || strings.Contains(m.Path, "..") {
			return fmt.Errorf("markers 中的路径 %q 必须是树内相对路径", m.Path)
		}
		if !sha256Pattern.MatchString(m.SHA256) {
			return fmt.Errorf("markers 中的条目 %q 缺少合法 sha256", m.Path)
		}
	}
	if f.Adapter.Source == "" || f.Adapter.Package == "" {
		return fmt.Errorf("adapter.source 与 adapter.package 都是必填项")
	}
	if !isGoIdentifier(f.Adapter.Package) {
		return fmt.Errorf("adapter.package %q 不是合法的 Go 包名", f.Adapter.Package)
	}
	return nil
}

func isGoIdentifier(s string) bool {
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return s != ""
}

func validateArtifact(a artifact) error {
	if a.Filename == "" || strings.ContainsAny(a.Filename, `/\`) || a.Filename == "." || a.Filename == ".." {
		return fmt.Errorf("filename %q 必须是单个文件名", a.Filename)
	}
	if !strings.HasPrefix(a.URL, "https://") {
		return fmt.Errorf("url %q 必须使用 https", a.URL)
	}
	if !sha256Pattern.MatchString(a.SHA256) {
		return fmt.Errorf("sha256 %q 不是 64 位小写十六进制", a.SHA256)
	}
	if a.Size <= 0 {
		return fmt.Errorf("size %d 必须为正数", a.Size)
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func inspectHost() (hostInfo, error) {
	info := hostInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	goBin, err := exec.LookPath("go")
	if err != nil {
		info.GoVersion = "未找到宿主 go"
		return info, failf(exitHost, "找不到宿主 go 命令：%v；准备阶段需要宿主 Go 运行本入口", err)
	}
	abs, err := filepath.Abs(goBin)
	if err != nil {
		return info, failf(exitInternal, "无法解析宿主 go 路径 %q：%v", goBin, err)
	}
	info.GoBinary = abs
	version, err := runCapture(abs, "version")
	if err != nil {
		return info, failf(exitHost, "宿主 go version 执行失败：%v", err)
	}
	info.GoVersion = version
	goroot, err := runCapture(abs, "env", "GOROOT")
	if err != nil {
		return info, failf(exitHost, "宿主 go env GOROOT 执行失败：%v", err)
	}
	info.GOROOT = goroot
	return info, nil
}

// runCapture 执行命令并返回去掉行尾的 stdout；环境变量中移除 GOROOT，
// 让被调用的 go 使用自身位置，避免宿主设置影响隔离副本。
func runCapture(bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOROOT=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GOTOOLCHAIN=local")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("%v：%s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func prepareOne(kind string, a artifact, name string, lock *lockFile, downloadDir, toolchainDir, manifestDir string, force, quiet bool) (*install, error) {
	finalDir := filepath.Join(toolchainDir, name)
	manifestPath := filepath.Join(manifestDir, name+".json")

	if _, err := os.Stat(finalDir); err == nil {
		inst, err := verifyExisting(finalDir, manifestPath, kind, name, a, lock)
		if err == nil {
			if !quiet {
				fmt.Printf("复用已准备的 %s：%s\n", kindLabel(kind), finalDir)
			}
			return inst, nil
		}
		if !force {
			return nil, failf(exitVerify, "%s 目录 %s 不能复用：%v。删除该目录或加 --force 重新准备。", kindLabel(kind), finalDir, err)
		}
		if !quiet {
			fmt.Printf("丢弃不可复用的 %s 目录 %s：%v\n", kindLabel(kind), finalDir, err)
		}
		if err := os.RemoveAll(finalDir); err != nil {
			return nil, failf(exitFS, "无法删除目录 %s：%v", finalDir, err)
		}
		if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
			return nil, failf(exitFS, "无法删除清单 %s：%v", manifestPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, failf(exitFS, "无法检查目录 %s：%v", finalDir, err)
	}

	archivePath, downloaded, err := ensureArchive(a, downloadDir, force, quiet)
	if err != nil {
		return nil, err
	}
	if !downloaded && !quiet {
		fmt.Printf("复用已校验的下载：%s\n", archivePath)
	}

	staging := finalDir + ".partial"
	if err := os.RemoveAll(staging); err != nil {
		return nil, failf(exitFS, "无法清理临时目录 %s：%v", staging, err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, failf(exitFS, "无法创建临时目录 %s：%v", staging, err)
	}
	if err := extractTarGz(archivePath, staging); err != nil {
		os.RemoveAll(staging)
		return nil, failf(exitFS, "解压 %s 失败：%v", archivePath, err)
	}
	root := filepath.Join(staging, "go")
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		os.RemoveAll(staging)
		return nil, failf(exitVerify, "压缩包 %s 中没有顶层的 go/ 目录", archivePath)
	}
	tools, stamp, err := verifyTree(kind, root, lock)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if err := os.Rename(root, finalDir); err != nil {
		os.RemoveAll(staging)
		return nil, failf(exitFS, "无法把 %s 发布为 %s：%v", root, finalDir, err)
	}
	os.RemoveAll(staging)
	for name, path := range tools {
		tools[name] = finalDir + strings.TrimPrefix(path, root)
	}

	mf := manifest{
		SchemaVersion: 1,
		Name:          name,
		Kind:          kind,
		Version:       lock.Go.Version,
		VersionStamp:  stamp,
		Target:        lock.Targets.Host,
		Archive:       a,
		InstalledAt:   time.Now().Format(time.RFC3339),
		Tools:         tools,
	}
	if err := writeJSON(manifestPath, mf); err != nil {
		return nil, err
	}
	if !quiet {
		fmt.Printf("已准备 %s：%s\n", kindLabel(kind), finalDir)
	}
	return &install{Kind: kind, Name: name, Dir: finalDir, Version: lock.Go.Version, Stamp: stamp, Target: lock.Targets.Host, Archive: a, Tools: tools, Reused: false, Verified: true}, nil
}

func verifyExisting(finalDir, manifestPath, kind, name string, a artifact, lock *lockFile) (*install, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("缺少安装清单，目录不完整")
		}
		return nil, err
	}
	var mf manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("安装清单不可解析：%v", err)
	}
	if mf.SchemaVersion != 1 || mf.Name != name || mf.Kind != kind {
		return nil, fmt.Errorf("安装清单与目标不符（name=%q kind=%q）", mf.Name, mf.Kind)
	}
	if mf.Version != lock.Go.Version {
		return nil, fmt.Errorf("已安装版本 %s 与锁定版本 %s 不同", mf.Version, lock.Go.Version)
	}
	if mf.Archive.SHA256 != a.SHA256 || mf.Archive.Filename != a.Filename {
		return nil, fmt.Errorf("已安装内容来自 %s（sha256 %s），与锁文件 %s 不同", mf.Archive.Filename, shortSHA(mf.Archive.SHA256), a.Filename)
	}
	tools, stamp, err := verifyTree(kind, finalDir, lock)
	if err != nil {
		return nil, err
	}
	mf.Tools = tools
	mf.VersionStamp = stamp
	if err := writeJSON(manifestPath, mf); err != nil {
		return nil, err
	}
	return &install{Kind: kind, Name: name, Dir: finalDir, Version: lock.Go.Version, Stamp: stamp, Target: lock.Targets.Host, Archive: a, Tools: tools, Reused: true, Verified: true}, nil
}

// verifyTree 检查解压结果是否真的是锁定的那份内容，而不只看目录是否存在。
func verifyTree(kind, dir string, lock *lockFile) (map[string]string, string, error) {
	version, stamp, err := readVersionFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		return nil, "", err
	}
	if version != lock.Go.Version {
		return nil, "", failf(exitVerify, "%s 首行为 %q，与锁定版本 %s 不同", filepath.Join(dir, "VERSION"), version, lock.Go.Version)
	}
	licensePath := filepath.Join(dir, lock.Go.LicenseFile)
	if _, err := os.Stat(licensePath); err != nil {
		return nil, "", failf(exitVerify, "缺少许可证文件 %s：%v", licensePath, err)
	}

	switch kind {
	case "sdk":
		toolDir := filepath.Join(dir, "pkg", "tool", strings.ReplaceAll(lock.Targets.Host, "/", "_"))
		tools := map[string]string{}
		for _, name := range []string{"compile", "link", "asm"} {
			p := filepath.Join(toolDir, name)
			if info, err := os.Stat(p); err != nil || info.IsDir() {
				return nil, "", failf(exitVerify, "SDK 缺少工具 %s", p)
			}
			tools[name] = p
		}
		goBin := filepath.Join(dir, "bin", "go")
		if info, err := os.Stat(goBin); err != nil || info.IsDir() {
			return nil, "", failf(exitVerify, "SDK 缺少 %s", goBin)
		}
		tools["go"] = goBin
		out, err := runCapture(goBin, "version")
		if err != nil {
			return nil, "", failf(exitVerify, "%s version 执行失败：%v", goBin, err)
		}
		want := fmt.Sprintf("go version %s %s", lock.Go.Version, lock.Targets.Host)
		if out != want {
			return nil, "", failf(exitVerify, "隔离 SDK 版本为 %q，期望 %q", out, want)
		}
		return tools, stamp, nil
	case "src":
		for _, rel := range []string{"src/cmd/compile", "src/cmd/link", "src/runtime"} {
			if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
				return nil, "", failf(exitVerify, "源码树缺少目录 %s", rel)
			}
		}
		return nil, stamp, nil
	default:
		return nil, "", failf(exitInternal, "未知的安装类别 %q", kind)
	}
}

// readVersionFile 读取 Go 发行包的 VERSION 文件。首行是版本号，第二行是可选的构建时间。
func readVersionFile(path string) (version, stamp string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", failf(exitVerify, "读取 %s 失败：%v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", failf(exitVerify, "%s 为空", path)
	}
	version = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		stamp = strings.TrimSpace(lines[1])
	}
	return version, stamp, nil
}

func ensureArchive(a artifact, dir string, force, quiet bool) (string, bool, error) {
	path := filepath.Join(dir, a.Filename)
	if force {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", false, failf(exitFS, "无法删除缓存 %s：%v", path, err)
		}
	}
	if info, err := os.Stat(path); err == nil {
		if err := verifyFile(path, a, info.Size()); err != nil {
			return "", false, failf(exitHash, "缓存文件 %s 校验失败：%v。删除该文件或加 --force 重新下载。", path, err)
		}
		return path, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, failf(exitFS, "无法检查缓存 %s：%v", path, err)
	}

	if !quiet {
		fmt.Printf("下载 %s（%.1f MiB）→ %s\n", a.URL, float64(a.Size)/(1<<20), path)
	}
	tmp := path + ".partial"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, failf(exitFS, "无法创建下载目录 %s：%v", dir, err)
	}
	out, err := os.Create(tmp)
	if err != nil {
		return "", false, failf(exitFS, "无法创建 %s：%v", tmp, err)
	}
	defer out.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		os.Remove(tmp)
		return "", false, failf(exitNet, "构造下载请求失败：%v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		os.Remove(tmp)
		return "", false, failf(exitNet, "下载 %s 失败：%v", a.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		os.Remove(tmp)
		return "", false, failf(exitNet, "下载 %s 返回 HTTP %d", a.URL, resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), resp.Body)
	if err != nil {
		os.Remove(tmp)
		return "", false, failf(exitNet, "读取 %s 的响应失败：%v", a.URL, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", false, failf(exitFS, "写入 %s 失败：%v", tmp, err)
	}
	if n != a.Size {
		os.Remove(tmp)
		return "", false, failf(exitHash, "下载长度 %d 与锁文件 %d 不同", n, a.Size)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != a.SHA256 {
		os.Remove(tmp)
		return "", false, failf(exitHash, "下载内容 sha256 为 %s，与锁文件 %s 不同", sum, a.SHA256)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", false, failf(exitFS, "无法发布下载文件 %s：%v", path, err)
	}
	if !quiet {
		fmt.Printf("校验 %s 通过（sha256 %s）\n", a.Filename, shortSHA(a.SHA256))
	}
	return path, true, nil
}

// verifyPatchFiles 检查锁文件登记的接入文件存在且内容匹配。首版补丁列表为空，
// 该函数在 01.05 加入 overlay 或补丁后开始生效。
func verifyPatchFiles(root string, lock *lockFile) ([]string, error) {
	paths := make([]string, 0, len(lock.Bridge.PatchFiles))
	for _, p := range lock.Bridge.PatchFiles {
		path := filepath.Join(root, filepath.FromSlash(p.Path))
		info, err := os.Stat(path)
		if err != nil {
			return nil, failf(exitVerify, "接入文件 %s 不可访问：%v", p.Path, err)
		}
		if info.IsDir() {
			return nil, failf(exitVerify, "接入文件 %s 是目录", p.Path)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			return nil, failf(exitFS, "读取接入文件 %s 失败：%v", p.Path, err)
		}
		if sum != p.SHA256 {
			return nil, failf(exitVerify, "接入文件 %s 的 sha256 为 %s，与锁文件 %s 不同", p.Path, sum, p.SHA256)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyFile(path string, a artifact, size int64) error {
	if size != a.Size {
		return fmt.Errorf("文件大小 %d 与锁文件 %d 不同", size, a.Size)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if sum != a.SHA256 {
		return fmt.Errorf("sha256 为 %s，与锁文件 %s 不同", sum, a.SHA256)
	}
	return nil
}

func extractTarGz(archivePath, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("不是有效的 gzip 文件：%w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取归档条目失败：%w", err)
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("归档条目 %q 越出目标目录", hdr.Name)
		}
		target := filepath.Join(dst, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("归档条目 %q 的符号链接指向绝对路径", hdr.Name)
			}
			rel := filepath.Clean(filepath.Join(filepath.Dir(name), filepath.FromSlash(hdr.Linkname)))
			if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("归档条目 %q 的符号链接越出目标目录", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// 其他类型不写入目标目录。
		}
	}
}

func checkRemoteTag(lock *lockFile) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return failf(exitNet, "找不到 git，无法复核远程标签：%v", err)
	}
	cmd := exec.Command(git, "ls-remote", "--tags", "https://github.com/golang/go", lock.Go.SourceTag)
	out, err := cmd.Output()
	if err != nil {
		return failf(exitNet, "git ls-remote 失败：%v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return failf(exitVerify, "远程没有标签 %s", lock.Go.SourceTag)
	}
	if fields[0] != lock.Go.SourceCommit {
		return failf(exitVerify, "标签 %s 在远程指向 %s，锁文件记录 %s", lock.Go.SourceTag, fields[0], lock.Go.SourceCommit)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return failf(exitInternal, "无法序列化 %s：%v", path, err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return failf(exitFS, "无法写入 %s：%v", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return failf(exitFS, "无法发布 %s：%v", path, err)
	}
	return nil
}

func printSummary(rep *report, reportPath string, lock *lockFile) {
	fmt.Println()
	if len(rep.Installs) == 0 {
		fmt.Printf("前端准备完成：%s\n", lock.Frontend.Version)
		fmt.Printf("  上游提交      %s（tag %s，许可证 %s）\n", lock.Frontend.Commit, lock.Frontend.Version, lock.Frontend.License)
	} else {
		fmt.Printf("工具链准备完成：%s（%s）\n", lock.Go.Version, lock.Targets.Host)
		fmt.Printf("  锁文件提交    %s（tag %s，许可证 %s）\n", lock.Go.SourceCommit, lock.Go.SourceTag, lock.Go.License)
	}
	for _, inst := range rep.Installs {
		state := "新准备"
		if inst.Reused {
			state = "已存在并复核"
		}
		stamp := ""
		if inst.Stamp != "" {
			stamp = "，" + inst.Stamp
		}
		fmt.Printf("  %-4s 目录   %s（%s%s）\n", kindLabel(inst.Kind), inst.Dir, state, stamp)
		keys := make([]string, 0, len(inst.Tools))
		for k := range inst.Tools {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("    %-8s %s\n", k, inst.Tools[k])
		}
	}
	if rep.Frontend != nil {
		state := "新准备"
		if rep.Frontend.Reused {
			state = "已存在并复核"
		}
		fmt.Printf("  前端          %s（%s，%s）\n", rep.Frontend.Version, rep.Frontend.Dir, state)
	}
	fmt.Printf("  宿主 Go       %s（%s，未修改，GOROOT %s）\n", rep.Host.GoBinary, rep.Host.GoVersion, rep.Host.GOROOT)
	fmt.Printf("  报告          %s\n", reportPath)
	if len(rep.Installs) > 0 && rep.Installs[0].Kind == "sdk" {
		sdk := rep.Installs[0]
		fmt.Println()
		fmt.Println("只在需要的 shell 中显式启用隔离工具链：")
		fmt.Printf("  export GOROOT=%q\n", sdk.Dir)
		fmt.Printf("  export PATH=%q:$PATH\n", filepath.Join(sdk.Dir, "bin"))
	}
}

func kindLabel(kind string) string {
	switch kind {
	case "sdk":
		return "SDK"
	case "frontend":
		return "前端"
	}
	return "源码"
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ---- 前端源码 ----

// prepareFrontend 解压并校验 TypeScript 前端源码，再把适配包复制进去。
//
// 上游模块的包全部位于 internal 下，模块外的代码不能导入。适配包因此必须
// 成为这棵树的一部分：仓库里的 bridge/tsgo/adapter 是唯一源文件，每次准备都
// 覆盖到 <tree>/tyneladapter，避免 .work 里的副本被当成可编辑的代码。
func prepareFrontend(root string, lock *lockFile, downloadDir, frontendDir, manifestDir string, force, verifyOnly, quiet bool) (*install, error) {
	f := lock.Frontend
	finalDir := filepath.Join(frontendDir, f.Name)
	manifestPath := filepath.Join(manifestDir, f.Name+".json")
	adapterSrc := filepath.Join(root, filepath.FromSlash(f.Adapter.Source))

	if verifyOnly {
		if err := verifyFrontendTree(finalDir, f); err != nil {
			return nil, err
		}
		if err := verifyAdapterCopy(adapterSrc, finalDir, f.Adapter.Package); err != nil {
			return nil, err
		}
		return &install{Kind: "frontend", Name: f.Name, Dir: finalDir, Version: f.Version, Target: lock.Targets.Host, Archive: f.Archive, Reused: true, Verified: true}, nil
	}

	reused := false
	if info, err := os.Stat(finalDir); err == nil {
		if !info.IsDir() {
			return nil, failf(exitVerify, "前端源码路径 %s 不是目录", finalDir)
		}
		if err := verifyFrontendTree(finalDir, f); err == nil {
			reused = true
		} else if !force {
			return nil, failf(exitVerify, "前端源码目录 %s 不能复用：%v。删除该目录或加 --force 重新准备。", finalDir, err)
		} else {
			if !quiet {
				fmt.Printf("丢弃不可复用的前端源码目录 %s：%v\n", finalDir, err)
			}
			if err := os.RemoveAll(finalDir); err != nil {
				return nil, failf(exitFS, "无法删除目录 %s：%v", finalDir, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, failf(exitFS, "无法检查目录 %s：%v", finalDir, err)
	}

	if reused {
		if !quiet {
			fmt.Printf("复用已准备的 %s：%s\n", kindLabel("frontend"), finalDir)
		}
	} else {
		archivePath, downloaded, err := ensureArchive(f.Archive, downloadDir, force, quiet)
		if err != nil {
			return nil, err
		}
		if !downloaded && !quiet {
			fmt.Printf("复用已校验的下载：%s\n", archivePath)
		}

		staging := finalDir + ".partial"
		if err := os.RemoveAll(staging); err != nil {
			return nil, failf(exitFS, "无法清理临时目录 %s：%v", staging, err)
		}
		if err := os.MkdirAll(staging, 0o755); err != nil {
			return nil, failf(exitFS, "无法创建临时目录 %s：%v", staging, err)
		}
		if err := extractTarGz(archivePath, staging); err != nil {
			os.RemoveAll(staging)
			return nil, failf(exitFS, "解压 %s 失败：%v", archivePath, err)
		}
		extracted := filepath.Join(staging, f.ArchiveRoot)
		if info, err := os.Stat(extracted); err != nil || !info.IsDir() {
			os.RemoveAll(staging)
			return nil, failf(exitVerify, "压缩包 %s 中没有顶层目录 %s", archivePath, f.ArchiveRoot)
		}
		if err := verifyFrontendTree(extracted, f); err != nil {
			os.RemoveAll(staging)
			return nil, err
		}
		if err := os.Rename(extracted, finalDir); err != nil {
			os.RemoveAll(staging)
			return nil, failf(exitFS, "无法把 %s 发布为 %s：%v", extracted, finalDir, err)
		}
		os.RemoveAll(staging)
		if !quiet {
			fmt.Printf("已准备 %s：%s\n", kindLabel("frontend"), finalDir)
		}
	}

	tools, err := syncAdapter(adapterSrc, finalDir, f.Adapter.Package)
	if err != nil {
		return nil, err
	}

	mf := manifest{
		SchemaVersion: 1,
		Name:          f.Name,
		Kind:          "frontend",
		Version:       f.Version,
		VersionStamp:  f.Commit,
		Target:        lock.Targets.Host,
		Archive:       f.Archive,
		InstalledAt:   time.Now().Format(time.RFC3339),
		Tools:         tools,
	}
	if err := writeJSON(manifestPath, mf); err != nil {
		return nil, err
	}
	return &install{Kind: "frontend", Name: f.Name, Dir: finalDir, Version: f.Version, Stamp: f.Commit, Target: lock.Targets.Host, Archive: f.Archive, Tools: tools, Reused: reused, Verified: true}, nil
}

// verifyFrontendTree 判定一棵解压出的源码树是否就是锁文件登记的那一份。
//
// 判定不靠目录名或时间戳：模块行、go 指令、四个标记文件的 sha256 和声明库的
// 规模都要对得上。少了任何一项，后续编译出的前端就可能不是锁文件描述的那一个。
func verifyFrontendTree(dir string, f lockFrontend) error {
	info, err := os.Stat(dir)
	if err != nil {
		return failf(exitVerify, "前端源码目录 %s 不可访问：%v", dir, err)
	}
	if !info.IsDir() {
		return failf(exitVerify, "前端源码路径 %s 不是目录", dir)
	}

	gomodPath := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(gomodPath)
	if err != nil {
		return failf(exitVerify, "缺少 %s：%v", gomodPath, err)
	}
	module, directive := parseGoMod(string(data))
	if module != f.Module {
		return failf(exitVerify, "源码身份不符：%s 的模块是 %q，锁文件是 %q", gomodPath, module, f.Module)
	}
	if directive != f.GoDirective {
		return failf(exitVerify, "源码身份不符：%s 的 go 指令是 %q，锁文件是 %q", gomodPath, directive, f.GoDirective)
	}
	if strings.Contains(string(data), "\nreplace ") || strings.Contains(string(data), "\nreplace(") {
		return failf(exitVerify, "%s 含 replace 指令，源码可能被替换过", gomodPath)
	}

	for _, m := range f.Markers {
		path := filepath.Join(dir, filepath.FromSlash(m.Path))
		info, err := os.Stat(path)
		if err != nil {
			return failf(exitVerify, "源码身份不符：缺少 %s", m.Path)
		}
		if info.IsDir() {
			return failf(exitVerify, "源码身份不符：%s 是目录", m.Path)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			return failf(exitFS, "读取 %s 失败：%v", m.Path, err)
		}
		if sum != m.SHA256 {
			return failf(exitVerify, "源码身份不符：%s 的 sha256 为 %s，与锁文件 %s 不同", m.Path, sum, m.SHA256)
		}
	}

	// 声明库由 go:embed 编进二进制。缺文件时构建会报 embed 匹配不到文件，
	// 这里提前给出能读懂的原因，而不是等到编译阶段才暴露。
	libDir := filepath.Join(dir, "internal", "bundled", "libs")
	entries, err := os.ReadDir(libDir)
	if err != nil {
		return failf(exitVerify, "缺少声明库目录 %s：%v", libDir, err)
	}
	count := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "lib.") && strings.HasSuffix(name, ".d.ts") {
			count++
		}
	}
	if count < 100 {
		return failf(exitVerify, "声明库不完整：%s 只有 %d 个 lib.*.d.ts", libDir, count)
	}
	return nil
}

func parseGoMod(text string) (module, directive string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case module == "" && strings.HasPrefix(line, "module "):
			module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
		case directive == "" && strings.HasPrefix(line, "go "):
			directive = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	return module, directive
}

// syncAdapter 把仓库里的适配包源码复制进上游源码树，返回文件名到 sha256 的映射。
//
// 复制是单向的：树里的旧副本会被覆盖，源目录已删除的文件也会从树里移除，否则
// 上一次留下的文件仍会参与编译。只有 .go 文件被复制，树里的其他内容不动。
func syncAdapter(srcDir, treeDir, pkg string) (map[string]string, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, failf(exitFS, "无法读取适配包源码目录 %s：%v", srcDir, err)
	}
	dstDir := filepath.Join(treeDir, pkg)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, failf(exitFS, "无法创建目录 %s：%v", dstDir, err)
	}

	want := make(map[string]bool)
	tools := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return nil, failf(exitFS, "读取适配包源码 %s 失败：%v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, e.Name()), data, 0o644); err != nil {
			return nil, failf(exitFS, "写入适配包 %s 失败：%v", e.Name(), err)
		}
		want[e.Name()] = true
		tools[e.Name()] = sha256Hex(data)
	}
	if len(tools) == 0 {
		return nil, failf(exitVerify, "适配包源码目录 %s 里没有 .go 文件", srcDir)
	}

	installed, err := os.ReadDir(dstDir)
	if err != nil {
		return nil, failf(exitFS, "无法读取 %s：%v", dstDir, err)
	}
	for _, e := range installed {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || want[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dstDir, e.Name())); err != nil {
			return nil, failf(exitFS, "无法删除旧副本 %s：%v", e.Name(), err)
		}
	}
	return tools, nil
}

// verifyAdapterCopy 在只读模式下比较树里的副本与仓库源文件，不写入任何东西。
func verifyAdapterCopy(srcDir, treeDir, pkg string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return failf(exitFS, "无法读取适配包源码目录 %s：%v", srcDir, err)
	}
	dstDir := filepath.Join(treeDir, pkg)
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		count++
		src, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return failf(exitFS, "读取适配包源码 %s 失败：%v", e.Name(), err)
		}
		dst, err := os.ReadFile(filepath.Join(dstDir, e.Name()))
		if err != nil {
			return failf(exitVerify, "适配包副本缺少 %s；运行 ./tools/toolchain/prep --frontend-only 重新同步", e.Name())
		}
		if !bytes.Equal(src, dst) {
			return failf(exitVerify, "适配包副本 %s 与仓库源文件不同；运行 ./tools/toolchain/prep --frontend-only 重新同步", e.Name())
		}
	}
	if count == 0 {
		return failf(exitVerify, "适配包源码目录 %s 里没有 .go 文件", srcDir)
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// checkRemoteFrontend 用 GitHub API 复核锁定的标签仍指向锁定的提交。
//
// 这里不用 git ls-remote：本机访问 github.com 的 git 通道会被代理掐断，而
// API 走普通 HTTPS 可用。复核只回答「上游标签有没有被移动」，不改变本地源码。
func checkRemoteFrontend(lock *lockFile) error {
	f := lock.Frontend
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", f.Repository, f.Version)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return failf(exitNet, "构造前端复核请求失败：%v", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tynel-prep")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return failf(exitNet, "复核前端标签 %s 失败：%v", f.Version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return failf(exitNet, "复核前端标签 %s 返回 HTTP %d", f.Version, resp.StatusCode)
	}
	var body struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return failf(exitNet, "解析前端复核响应失败：%v", err)
	}
	if body.SHA != f.Commit {
		return failf(exitVerify, "上游标签 %s 现在指向 %s，锁文件是 %s；上游已移动，需要人工确认后再更新锁文件", f.Version, body.SHA, f.Commit)
	}
	return nil
}
