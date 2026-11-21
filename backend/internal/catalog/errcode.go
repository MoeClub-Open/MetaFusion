package catalog

// 错误响应的"原文不外发"判定。
//
// 目录服务的错误码有约定形状：store/validation 层用 "码" 或 "码: 细节" 表达业务错误
// （unknown_field: duration、group_not_found: member …），前端按冒号分段取码翻译。
// 库层/驱动/网络/反序列化的原文则不是码：它们带空格、大写、斜杠或引号
// （"dial tcp 10.0.0.5:5432: connect: connection refused"、
// "invalid character 'x' looking for beginning of value"、"open /etc/passwd: permission denied"）。
//
// 因此 respond 的默认分支不能把 err.Error() 当码回给客户端：非码原文一律 500 + 通用码，
// 原文只进服务端日志（2026-09-19 第二轮架构报告 #16：文档反序列化失败与连接被拒
// 都会以 400 把驱动原文交出去，而前端对未知码是原样渲染的）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

// apiDetailError attaches structured diagnostic context to a stable domain
// error code without changing err.Error() or introducing separate responders.
type apiDetailError struct {
	code    string
	details map[string]any
}

func (e *apiDetailError) Error() string { return e.code }

func errWithDetails(code string, details map[string]any) error {
	return &apiDetailError{code: code, details: details}
}

func errDetails(err error) map[string]any {
	var d *apiDetailError
	if errors.As(err, &d) && len(d.details) > 0 {
		return d.details
	}
	return nil
}

// codeInternalError 是"这不是业务错误、是服务端故障"的统一码。
const codeInternalError = "internal_error"

// machineCodePattern 是稳定机器码的形状：小写字母开头，只含 [a-z0-9_.-[] 这几个字符。
// 允许点与方括号是因为校验错误会把字段路径放在前面（attributes.foo、canonical_entries[0]）；
// 驱动与网络原文一定不满足形状（含空格、大写、斜杠、引号），因此形状判定本身就挡住了
// "把驱动原文当错误码回给客户端"。
var machineCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.\[\]-]*$`)

// infraErrorPrefixes 是标准库/驱动的错误前缀：它们与机器码同形
// （"sql: connection is already closed"、"driver: bad connection"），必须显式挡掉。
var infraErrorPrefixes = map[string]bool{
	"sql": true, "driver": true, "pq": true, "pgx": true,
	"context": true, "net": true, "tls": true, "x509": true,
	"io": true, "os": true, "fs": true, "http": true,
}

// machineCode 判定首段是否是稳定机器码形状。
func machineCode(s string) bool { return machineCodePattern.MatchString(s) }

// firstCode 取错误文本的首段：store/validation 层用"码"或"码: 细节"表达业务错误。
func firstCode(message string) string {
	if i := strings.Index(message, ":"); i >= 0 {
		return strings.TrimSpace(message[:i])
	}
	return strings.TrimSpace(message)
}

// internalFailure 判定"这不是业务错误码，而是后端故障原文"。
func internalFailure(err error) bool {
	var pg *pq.Error
	if errors.As(err, &pg) {
		return true
	}
	// These standard decoding errors start with "json:", which otherwise
	// looks like a domain code and leaks Go struct/field names as a 400 response.
	var valueType *json.UnmarshalTypeError
	var syntax *json.SyntaxError
	if errors.As(err, &valueType) || errors.As(err, &syntax) {
		return true
	}
	for _, sentinel := range []error{
		context.Canceled, context.DeadlineExceeded,
		sql.ErrConnDone, sql.ErrTxDone, driver.ErrBadConn,
		io.EOF, io.ErrUnexpectedEOF,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	if infraErrorPrefixes[firstCode(err.Error())] {
		return true
	}
	return !machineCode(firstCode(err.Error()))
}
