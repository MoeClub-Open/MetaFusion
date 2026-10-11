package catalog

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// 查询字符串是唯一没有解码器的外部输入：gin 把 %00、%FF 这类字节原样交给 SQL，
// Postgres 在 ILIKE 上直接拒绝（SQLSTATE 22021），respond 只能把它归到 500
// database_error——用户输错参数被报成服务故障，前端又把它渲染成"没有结果"
// （2026-09-19 审计第 5、6 条）。本文件把"能不能下传"的判定前置到参数层：
// 非法 UTF-8、控制字符、超长值、解析不了的原始查询串一律 400 + 稳定机器码。

// listTextParamLimit 是文本过滤参数的长度上限（字节）。它只负责挡"拿超长串压库"，
// 取值远大于任何合法输入：题名子串、kind/type/status 码、标签名、UUID 都在几十字节内。
const listTextParamLimit = 256

// 参数类错误的稳定机器码（响应体统一是 {"error": <code>}，见 respond）。
const (
	codeInvalidQueryParam  = "invalid_query_param"
	codeQueryTooLong       = "query_too_long"
	codeInvalidLimit       = "invalid_limit"
	codeInvalidOffset      = "invalid_offset"
	codeInvalidPage        = "invalid_page"
	codePaginationConflict = "pagination_conflict"
)

// 分页默认值与上限：与 OpenAPI 声明的 limit 语义一致（default 50, max 100）。
// 前端 fetchAllPages 以 100 翻页，因此上限不能再低。
const (
	defaultListLimit = 50
	maxListLimit     = 100
)

// listTextParams 是列表端点会下传给 SQL 的文本查询参数。整数分页参数
// （limit/offset/page）不在此列，由 listPagination 单独判定。
var listTextParams = []string{
	"q", "kind", "kinds", "status", "sort", "order", "locale",
	"work_id", "content_unit_id", "release_id", "medium_id", "parent_id", "field", "value", "tags",
	"original_language", "has_pictures", "tags_mode",
}

// errParam 只携带机器码：respond 用 err.Error() 作为响应体里的 error 值，
// 参数名与原因不进响应（对匿名调用方没有额外信息价值）。
func errParam(code string) error { return errors.New(code) }

// checkQueryText 判定单个取值能否安全下传。
// 允许制表符：它可能是粘贴里的合法分隔符，且不破坏 SQL 文本；换行/回车则拒绝。
func checkQueryText(v string) error {
	if len(v) > listTextParamLimit {
		return errParam(codeQueryTooLong)
	}
	if !utf8.ValidString(v) {
		return errParam(codeInvalidQueryParam)
	}
	for _, r := range v {
		if r == '	' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return errParam(codeInvalidQueryParam)
		}
	}
	return nil
}

// validateRawQuery 先看原始查询串本身能否解析：裸 % 这类写法会让 net/url 静默丢掉
// 该参数（?q=100% 取到的 q 是空串），于是"输入非法"被吞成"参数不存在"。
// 解析失败即 400，而不是让调用方以为筛选生效了。
func validateRawQuery(c *gin.Context) error {
	if _, err := url.ParseQuery(c.Request.URL.RawQuery); err != nil {
		return errParam(codeInvalidQueryParam)
	}
	return nil
}

// validateTextQuery 校验一组文本查询参数（可重复出现，逐个取值校验）。
func validateTextQuery(c *gin.Context, names ...string) error {
	if err := validateRawQuery(c); err != nil {
		return err
	}
	for _, name := range names {
		for _, v := range c.QueryArray(name) {
			if err := checkQueryText(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// listPagination 解析并归一 limit/offset/page。
//
// 契约（2026-09-19 定，随 OpenAPI 一起发布）：
//   - limit 与 offset 是基本分页：limit 缺省 50、允许 1..100；offset 缺省 0、必须 ≥ 0。
//   - page（1 起）是便捷写法，等价于 offset=(page-1)*limit；page 必须 ≥ 1。
//   - page 与 offset 同时出现即 400 pagination_conflict，而不是让其中一个悄悄生效：
//     调用方给了两个互相矛盾的分页意图，服务端替它挑一个正是本次要修的那类静默行为
//     （同 normalizeListSort 对未知排序键返回 400 的理由——静默忽略会让人以为参数生效了）。
//   - 非整数（abc/1.5/空格）与越界值一律 400，不再静默回落到默认值：线上实测
//     limit=-1、limit=abc 都拿到 200 + 50 条，调用方无法察觉自己的分页没生效。
//   - 空串（?limit=&offset=）按"未提供"处理：URL 拼装里常见的占位写法，不带任何意图。
func listPagination(c *gin.Context) (limit, offset int, err error) {
	limit = defaultListLimit
	rawLimit := c.Query("limit")
	if rawLimit != "" {
		limit, err = strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxListLimit {
			return 0, 0, errParam(codeInvalidLimit)
		}
	}
	rawOffset, rawPage := c.Query("offset"), c.Query("page")
	if rawOffset != "" && rawPage != "" {
		return 0, 0, errParam(codePaginationConflict)
	}
	if rawPage != "" {
		page, perr := strconv.Atoi(rawPage)
		if perr != nil || page < 1 {
			return 0, 0, errParam(codeInvalidPage)
		}
		// (page-1)*limit 溢出即视为越界页：int 在本平台是 64 位，但仍要显式挡住，
		// 否则 OFFSET 会拿到一个回绕后的负数。
		if page-1 > math.MaxInt/limit {
			return 0, 0, errParam(codeInvalidPage)
		}
		return limit, (page - 1) * limit, nil
	}
	if rawOffset != "" {
		offset, err = strconv.Atoi(rawOffset)
		if err != nil || offset < 0 {
			return 0, 0, errParam(codeInvalidOffset)
		}
	}
	return limit, offset, nil
}
