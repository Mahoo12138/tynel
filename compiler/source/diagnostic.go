package source

import (
	"fmt"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// Severity 是诊断的严重程度，取值与前端一致。
type Severity string

const (
	SeverityError      Severity = "error"
	SeverityWarning    Severity = "warning"
	SeveritySuggestion Severity = "suggestion"
	SeverityMessage    Severity = "message"
)

// ParseSeverity 解析前端给的类别名。未知取值返回 ok=false：那说明适配层把
// 类别映射错了，继续猜一个值只会让错误的严重程度悄悄传下去。
func ParseSeverity(s string) (Severity, bool) {
	switch Severity(s) {
	case SeverityError, SeverityWarning, SeveritySuggestion, SeverityMessage:
		return Severity(s), true
	}
	return "", false
}

// Location 是一条诊断在快照里的位置。
//
// Bytes 保留原始字节区间，Start/End 是换算后的行列。两者都要留着：字节区间
// 用来切片和比对，行列只用来展示，不能拿行列反推字节。
type Location struct {
	FileID FileID   `json:"fileId"`
	Path   string   `json:"file"`
	Bytes  Range    `json:"bytes"`
	Start  Position `json:"start"`
	End    Position `json:"end"`
}

// Diagnostic 是一条带位置的源级诊断。
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     int32    `json:"code"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
}

// String 返回一行可读形式：路径:行:列: 消息 (TS 编号)。
func (d Diagnostic) String() string {
	where := d.Location.Path
	if where == "" {
		where = "<程序>"
	}
	if d.Location.Start.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s (TS%d)", where, d.Location.Start.Line, d.Location.Start.Column, d.Message, d.Code)
	}
	return fmt.Sprintf("%s: %s (TS%d)", where, d.Message, d.Code)
}

// Locate 把字节区间换算成完整位置。
func (s *Snapshot) Locate(r Range) (Location, error) {
	start, err := s.Position(r.Start)
	if err != nil {
		return Location{}, err
	}
	end, err := s.Position(r.End)
	if err != nil {
		return Location{}, err
	}
	return Location{
		FileID: s.id,
		Path:   s.path,
		Bytes:  r,
		Start:  start,
		End:    end,
	}, nil
}

// FrontendDiagnostic 是前端报告的一条诊断的原始形状。
//
// 这里刻意不使用适配层的类型：compiler/source 属于 Tynel 自己的模块，不依赖
// 上游源码树；宿主把适配层的字段填进来即可。Start 与 Length 是 UTF-8 字节。
type FrontendDiagnostic struct {
	File     string
	Code     int32
	Category string
	Message  string
	Start    int
	Length   int
}

// MapDiagnostics 把前端诊断绑定到快照位置。
//
// 指向其他文件（例如声明库）的诊断不参与换算，只保留路径：它们不属于这份
// 快照，硬套一个行列会把位置说错。落在快照内但区间非法的诊断会让整个映射
// 失败——那说明前端给的位置和这份快照对不上，此时停下比输出错位置好。
func (s *Snapshot) MapDiagnostics(in []FrontendDiagnostic) ([]Diagnostic, error) {
	out := make([]Diagnostic, 0, len(in))
	for _, raw := range in {
		severity, ok := ParseSeverity(raw.Category)
		if !ok {
			return nil, diagnostic.New(diagnostic.Internal, "source.bad-severity", "前端返回了未知的诊断类别 %q", raw.Category)
		}
		if raw.File != "" && raw.File != s.path {
			out = append(out, Diagnostic{
				Severity: severity,
				Code:     raw.Code,
				Message:  raw.Message,
				Location: Location{Path: raw.File},
			})
			continue
		}
		if raw.Start < 0 {
			// 程序级诊断没有源码位置；不要把它硬塞进入口快照。
			out = append(out, Diagnostic{
				Severity: severity,
				Code:     raw.Code,
				Message:  raw.Message,
				Location: Location{Path: s.path},
			})
			continue
		}
		r, err := s.Range(raw.Start, raw.Start+raw.Length)
		if err != nil {
			return nil, err
		}
		loc, err := s.Locate(r)
		if err != nil {
			return nil, err
		}
		out = append(out, Diagnostic{
			Severity: severity,
			Code:     raw.Code,
			Message:  raw.Message,
			Location: loc,
		})
	}
	return out, nil
}
