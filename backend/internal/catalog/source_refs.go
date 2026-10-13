package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// 操作级来源映射（source_refs）。
//
// 提交级 sources 是来源池；每条来源可带 key。每个操作用 source_refs 声明自己由哪些
// 来源支撑，可细到 JSON Pointer 字段路径。应用时该操作的修订只记录它引用的来源，
// 并在来源上写入 pointers，批量提交不再把整批来源混进每个条目的修订。
//
// 过渡期：未写 source_refs 的操作仍继承整个来源池，回执带 source_refs_missing 警告；
// 未被任何操作引用的带 key 来源给出 source_unused 警告。严格模式
// （CATALOG_STRICT_SOURCE_REFS=true 或 SetStrictSourceRefs(true)）把这两类警告升级为 400。

// CommitWarning 是不阻止提交的来源质量提示；Operation 为 nil 表示提交级。
type CommitWarning struct {
	Code      string `json:"code"`
	Operation *int   `json:"operation,omitempty"`
	Source    string `json:"source,omitempty"`
}

var strictSourceRefs atomic.Bool

func init() {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("CATALOG_STRICT_SOURCE_REFS"))); err == nil {
		strictSourceRefs.Store(v)
	}
}

// SetStrictSourceRefs 切换严格模式（过渡期结束后打开）。
func SetStrictSourceRefs(v bool) { strictSourceRefs.Store(v) }

func intPtr(i int) *int { return &i }

// pointerCovers 判断来源指针与改动路径是否同一字段链：相等、指针是路径前缀
// （来源支撑整个对象），或路径是指针前缀（整体替换的对象里由来源支撑其中一项）。
func pointerCovers(pointer, path string) bool {
	return pointer == path || strings.HasPrefix(path, pointer+"/") || strings.HasPrefix(pointer, path+"/")
}

func documentHasPointer(doc any, parts []string) bool {
	cur := doc
	for _, seg := range parts {
		switch x := cur.(type) {
		case map[string]any:
			v, ok := x[seg]
			if !ok {
				return false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) || strconv.Itoa(i) != seg {
				return false
			}
			cur = x[i]
		default:
			return false
		}
	}
	return true
}

// validateCommitSourceRefs 校验 key 与 source_refs，并返回过渡期警告。
func validateCommitSourceRefs(in CatalogCommit) ([]CommitWarning, error) {
	keys := map[string]bool{}
	for _, src := range in.Sources {
		if len(src.Pointers) != 0 {
			return nil, fmt.Errorf("source_pointers_not_allowed")
		}
		if src.Key == "" {
			continue
		}
		if !commitRef.MatchString(src.Key) {
			return nil, fmt.Errorf("invalid_source_key")
		}
		if keys[src.Key] {
			return nil, fmt.Errorf("duplicate_source_key")
		}
		keys[src.Key] = true
	}
	used := map[string]bool{}
	var warnings []CommitWarning
	for i, op := range in.Operations {
		fail := func(err error) error { return &commitOperationError{i, err} }
		if op.SourceRefs == nil {
			warnings = append(warnings, CommitWarning{Code: "source_refs_missing", Operation: intPtr(i)})
			// 过渡期继承整个来源池：池里的来源都视为被使用。
			for k := range keys {
				used[k] = true
			}
			continue
		}
		if len(op.SourceRefs) == 0 {
			return nil, fail(fmt.Errorf("source_refs_empty"))
		}
		var doc any
		if op.Action == "create" {
			if err := json.Unmarshal(op.Document, &doc); err != nil {
				return nil, fail(fmt.Errorf("invalid_document"))
			}
		}
		seenRef := map[string]bool{}
		for _, ref := range op.SourceRefs {
			if !keys[ref.Source] {
				return nil, fail(fmt.Errorf("unknown_source_ref"))
			}
			if seenRef[ref.Source] {
				return nil, fail(fmt.Errorf("duplicate_source_ref"))
			}
			seenRef[ref.Source] = true
			used[ref.Source] = true
			seenPointer := map[string]bool{}
			for _, ptr := range ref.Pointers {
				parts, err := pointerParts(ptr)
				if err != nil || seenPointer[ptr] {
					return nil, fail(fmt.Errorf("invalid_source_pointer"))
				}
				seenPointer[ptr] = true
				switch op.Action {
				case "create":
					if !documentHasPointer(doc, parts) {
						return nil, fail(fmt.Errorf("source_pointer_not_in_document"))
					}
				case "update":
					ok := false
					for _, p := range op.Patch {
						if pointerCovers(ptr, p.Path) {
							ok = true
							break
						}
					}
					if !ok {
						return nil, fail(fmt.Errorf("source_pointer_not_patched"))
					}
				}
			}
		}
	}
	for _, src := range in.Sources {
		if src.Key != "" && !used[src.Key] {
			warnings = append(warnings, CommitWarning{Code: "source_unused", Source: src.Key})
		}
	}
	if strictSourceRefs.Load() && len(warnings) > 0 {
		w := warnings[0]
		if w.Operation != nil {
			return nil, &commitOperationError{*w.Operation, fmt.Errorf("%s", w.Code)}
		}
		return nil, fmt.Errorf("%s", w.Code)
	}
	return warnings, nil
}

// operationSources 返回写入该操作修订的来源：有 source_refs 时只取被引用的来源并带上
// pointers；否则（过渡期）继承整个来源池。
func operationSources(in CatalogCommit, op CommitOperation) []Source {
	if op.SourceRefs == nil {
		return in.Sources
	}
	byKey := map[string]Source{}
	for _, s := range in.Sources {
		if s.Key != "" {
			byKey[s.Key] = s
		}
	}
	out := make([]Source, 0, len(op.SourceRefs))
	for _, ref := range op.SourceRefs {
		s := byKey[ref.Source]
		s.Pointers = append([]string(nil), ref.Pointers...)
		out = append(out, s)
	}
	return out
}
