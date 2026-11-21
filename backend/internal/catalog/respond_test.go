package catalog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// respond 的"错误码 → HTTP 状态码"映射。重点覆盖被 %w 包裹的 forbidden/version_conflict：
// 旧实现用 err.Error() 全等比较，包裹后 403/409 会退化成 400；以及 401 只能来自
// required() 闸门（invalid_credentials 分支已删，目录服务内没有生产者）。
func TestRespondMapsStatusByErrorChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"success", nil, 200, ""},
		{"no rows", sql.ErrNoRows, 404, "not_found"},
		{"forbidden", errForbidden, 403, "forbidden"},
		{"wrapped forbidden", fmt.Errorf("delivery_partial: 1/2 failed: %w", errForbidden), 403, "delivery_partial: 1/2 failed: forbidden"},
		{"doubly wrapped forbidden", fmt.Errorf("merge_relation_conflict: %w", fmt.Errorf("lifecycle: %w", errForbidden)), 403, "merge_relation_conflict: lifecycle: forbidden"},
		{"version conflict", errVersionConflict, 409, "version_conflict"},
		{"idempotency conflict", errIdempotencyConflict, 409, "idempotency_conflict"},
		{"wrapped version conflict", fmt.Errorf("attributes.x: %w", errVersionConflict), 409, "attributes.x: version_conflict"},
		{"domain code", errors.New("invalid_payload"), 400, "invalid_payload"},
		{"wrapped domain code keeps message", fmt.Errorf("attributes.foo: %w", errors.New("disabled_term")), 400, "attributes.foo: disabled_term"},
		{"pq unique violation", &pq.Error{Code: "23505"}, 400, "constraint_violation"},
		{"pq foreign key", &pq.Error{Code: "23503"}, 400, "constraint_violation"},
		{"pq invalid text representation", &pq.Error{Code: "22P02"}, 400, "invalid_id"},
		{"pq other", &pq.Error{Code: "08006"}, 500, "database_error"},
	} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		respond(ctx, gin.H{"ok": true}, tc.err)
		if w.Code != tc.wantStatus {
			t.Errorf("%s: status=%d want=%d (body=%s)", tc.name, w.Code, tc.wantStatus, w.Body.String())
		}
		if tc.err == nil {
			continue
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: bad body %s", tc.name, w.Body.String())
		}
		if body.Error != tc.wantCode {
			t.Errorf("%s: error=%q want=%q", tc.name, body.Error, tc.wantCode)
		}
	}
}

// leakPattern 是绝不允许出现在错误响应体里的形状：驱动前缀、SQLSTATE、SQL 片段、
// 表名/约束名、文件路径与内网地址（2026-09-19 第二轮架构报告 #16 的回归断言）。
var leakPattern = regexp.MustCompile(`pq:|SQLSTATE|SELECT|INSERT|UPDATE|DELETE|catalog\.entities|/|\\|dial tcp|127\.0\.0\.1|connection refused|permission denied|no such host`)

// 默认分支不再把 err.Error() 当错误码：非 pq 的库层错误（连接被拒、context 取消、连接已关）
// 与文档反序列化失败都必须回 500 + 通用码，且响应体里不得出现驱动原文。
func TestRespondHidesDriverText(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"net refused", errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")},
		{"no such host", errors.New("dial tcp: lookup auth on 10.0.0.2:53: no such host")},
		{"context canceled", context.Canceled},
		{"context deadline", context.DeadlineExceeded},
		{"conn done", sql.ErrConnDone},
		{"bad conn", driver.ErrBadConn},
		{"conn closed", errors.New("sql: database is closed")},
		{"json decode", errors.New("invalid character 'x' looking for beginning of value")},
		{"json field type", &json.UnmarshalTypeError{Value: "number", Type: reflect.TypeOf(""), Struct: "Entity", Field: "title"}},
		{"unexpected eof", io.ErrUnexpectedEOF},
		{"file path", errors.New("open /etc/metafusion/catalog.json: permission denied")},
		{"sql fragment", errors.New("SELECT id FROM catalog.entities WHERE id = $1")},
		{"pq detail", &pq.Error{Code: "42P01", Message: `relation "catalog.entities" does not exist`}},
	} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		respond(ctx, gin.H{"ok": true}, tc.err)
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: bad body %s", tc.name, w.Body.String())
		}
		if w.Code != 500 {
			t.Errorf("%s: status=%d want=500 (body=%s)", tc.name, w.Code, w.Body.String())
		}
		if body.Error != codeInternalError && body.Error != "database_error" {
			t.Errorf("%s: 未登记的故障必须回通用码，实际 %q", tc.name, body.Error)
		}
		if leakPattern.MatchString(w.Body.String()) {
			t.Errorf("%s: 响应体泄露了故障原文：%s", tc.name, w.Body.String())
		}
	}
}

// 业务码与"字段路径 + 码"的复合形态必须继续原样透出：前端按冒号分段取码翻译，
// 把它们误判成故障原文会让所有校验错误变成 500。
func TestRespondKeepsDomainCodes(t *testing.T) {
	for _, tc := range []string{
		"unknown_field: duration",
		"canonical_entries[0].duration: unknown_field",
		"four_locale_names_required: zh-TW,ja-JP",
		"undeclared_release_subject: release=x work=y",
		"invalid_attribute_value: work.duration=90",
	} {
		if internalFailure(errors.New(tc)) {
			t.Errorf("%q 是业务码（可能是路径前缀形式），不该被判成故障原文", tc)
		}
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		respond(ctx, gin.H{"ok": true}, errors.New(tc))
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: bad body %s", tc, w.Body.String())
		}
		if w.Code != 400 || body.Error != tc {
			t.Errorf("%q: status=%d code=%q", tc, w.Code, body.Error)
		}
	}
}

func TestRespondIncludesStructuredDetailsAndUnknownField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	err := checkImmutableScope("ent-1", Entity{Kind: "medium", ReleaseID: "rel-old"}, Entity{Kind: "medium", ReleaseID: ""})
	if err == nil || err.Error() != "immutable_scope" {
		t.Fatalf("expected immutable_scope, got %v", err)
	}
	respond(ctx, nil, err)
	var got map[string]any
	if decErr := json.Unmarshal(w.Body.Bytes(), &got); decErr != nil {
		t.Fatalf("decode failed: %v", decErr)
	}
	if w.Code != 400 || got["error"] != "immutable_scope" || got["entity_id"] != "ent-1" || got["field"] != "release_id" || got["old"] != "rel-old" || got["new"] != "" {
		t.Fatalf("unexpected detail payload: %d %+v", w.Code, got)
	}

	w2 := httptest.NewRecorder()
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	req := httptest.NewRequest("POST", "/api/catalog/entities/batch", strings.NewReader(`{"ids":[],"fields":["id"]}`))
	req.RemoteAddr = "198.51.100.44:12345"
	r.ServeHTTP(w2, req)
	var got2 map[string]any
	if decErr := json.Unmarshal(w2.Body.Bytes(), &got2); decErr != nil {
		t.Fatalf("decode batch error failed: %v", decErr)
	}
	if w2.Code != 400 || got2["error"] != "invalid_payload" || got2["unknown_field"] != "fields" {
		t.Fatalf("expected unknown_field=fields, got %d %+v", w2.Code, got2)
	}
}
