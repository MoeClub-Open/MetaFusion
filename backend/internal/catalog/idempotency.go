package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// 创建请求的持久幂等（R1）：目录自己的 PG 表 + 主键约束 + 与业务写入同一事务。
//
// 为什么不用进程内存（原 sync.Map 24h）：提交后进程退出、请求打到不同副本、
// 同键并发双建都会重复创建；同键不同载荷还会拿到旧结果。
// 本实现：键 = 操作/用户/请求键（见 IdempotencyClaim），行内同时存请求摘要与首创响应。
//   - 声明与业务写入同一事务：崩溃只会整体回滚，不存在"占了键没结果"的半成品；
//   - 同键并发：后来者在主键上阻塞，先行者提交后按已存在行重放，回滚则正常插入；
//   - 双副本/重启：读同一张表，同结果；
//   - 同键不同载荷：摘要不同，409 idempotency_conflict（不静默返回旧结果）。
//
// 保留策略见 000006 迁移头注释。
const (
	// IdempotencyOpEntityCreate 是 POST /catalog/entities 的幂等操作名。
	IdempotencyOpEntityCreate = "entity.create"
	// IdempotencyOpRelationCreate 是 POST /catalog/relations 的幂等操作名。
	IdempotencyOpRelationCreate = "relation.create"
)

// errIdempotencyConflict 是同键不同载荷的稳定码（HTTP 409，见 respond）。
// 与 version_conflict 同状态不同码：前者是"请求键复用了不同内容"，后者是"版本过期"。
var errIdempotencyConflict = errors.New("idempotency_conflict")

// errIdempotentReplay 是内部控制流：事务内发现已存在响应时提前返回，Save/SaveRelation
// 把它翻译成"返回重放结果、nil 错误"，永不外发（respond  mapping 无此分支）。
var errIdempotentReplay = errors.New("idempotent_replay")

// IdempotencyClaim 是一次创建请求的幂等声明：HTTP 层在 body 解码后填充（见 http.go），
// Store 层在业务事务内声明/回填。Edit.idempotency 未导出是有意的：JSON 解码不填充
// 未导出字段，客户端无法伪造（DisallowUnknownFields 会把同名 JSON 键判为未知字段）。
type IdempotencyClaim struct {
	Operation string
	UserID    string
	Key       string
	Hash      string
}

// requestHash 算请求摘要：encoding/json 对 map 键排序，同一逻辑载荷的哈希稳定；
// 字段顺序/语义变化即不同摘要，触发 409 而不是静默复用。
func requestHash(v any) string {
	sum := sha256.Sum256([]byte(encode(v)))
	return hex.EncodeToString(sum[:])
}

// claimIdempotencyTx 在业务事务内声明幂等：
// 插入成功返回 claimed=true，调用方继续执行业务写入并在提交前回填响应；
// 键已存在且摘要一致返回已存响应（claimed=false，重放，不再写业务）；
// 键已存在但摘要不同返回 errIdempotencyConflict。
func claimIdempotencyTx(ctx context.Context, tx *sql.Tx, c *IdempotencyClaim) (prior []byte, claimed bool, err error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO catalog.idempotency_keys(operation,user_id,request_key,request_hash,response) VALUES($1,$2,$3,$4,'null') ON CONFLICT DO NOTHING`, c.Operation, c.UserID, c.Key, c.Hash)
	if err != nil {
		return nil, false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil, true, nil
	}
	var hash string
	var resp []byte
	if err = tx.QueryRowContext(ctx, `SELECT request_hash,response FROM catalog.idempotency_keys WHERE operation=$1 AND user_id=$2 AND request_key=$3`, c.Operation, c.UserID, c.Key).Scan(&hash, &resp); err != nil {
		return nil, false, err
	}
	if hash != c.Hash {
		return nil, false, errIdempotencyConflict
	}
	// 占位 'null' 不应出现在已提交行：声明与回填同一事务，要么一起提交（必有真实响应），
	// 要么一起回滚（行不存在）。出现即内部不一致，大声失败，不返回零值实体。
	if len(resp) == 0 || string(resp) == "null" {
		return nil, false, fmt.Errorf("idempotency_incomplete: %s", c.Operation)
	}
	return resp, false, nil
}

// setIdempotencyResponseTx 在同一事务内回填首创响应（调用方在业务写入成功后调用）。
func setIdempotencyResponseTx(ctx context.Context, tx *sql.Tx, c *IdempotencyClaim, response any) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog.idempotency_keys SET response=$4 WHERE operation=$1 AND user_id=$2 AND request_key=$3`, c.Operation, c.UserID, c.Key, encode(response))
	return err
}

// replayEntity 把已存响应解回实体（重放路径，见 Save 内的 claim 调用点）。
func replayEntity(prior []byte) (Entity, error) {
	var e Entity
	if err := json.Unmarshal(prior, &e); err != nil {
		return Entity{}, err
	}
	return e, nil
}

// replayRelation 把已存响应解回关系（重放路径，见 SaveRelation 内的 claim 调用点）。
func replayRelation(prior []byte) (Relation, error) {
	var r Relation
	if err := json.Unmarshal(prior, &r); err != nil {
		return Relation{}, err
	}
	return r, nil
}
