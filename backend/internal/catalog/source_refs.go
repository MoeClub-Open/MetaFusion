package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 操作级来源映射（source_refs）。
//
// 提交级 sources 是来源池，每条来源必须带唯一 key。每个操作必须用 source_refs 声明
// 自己由哪些来源支撑，可细到 JSON Pointer 字段路径；应用时该操作的修订只记录它引用的
// 来源，并在来源上写入 pointers。没有过渡期、没有兼容路径：
//   - 操作缺 source_refs（或为空）→ 400 source_refs_required
//   - 来源缺 key → 400 source_key_required
//   - 池里有来源未被任何操作引用 → 400 source_unused
//
// 历史修订（本功能之前写入）没有逐操作指针，由一次性迁移标记为 scope="commit"
// （见 SourceScopeCommit），只在读取/展示时出现；新提交里客户端不得自带 scope。

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

// validateCommitSourceRefs 校验来源池与每个操作的 source_refs；任何不完整的证据映射都是错误。
func validateCommitSourceRefs(in CatalogCommit) error {
	keys := map[string]bool{}
	for _, src := range in.Sources {
		if len(src.Pointers) != 0 {
			return fmt.Errorf("source_pointers_not_allowed")
		}
		if src.Scope != "" {
			return fmt.Errorf("source_scope_not_allowed")
		}
		if src.Key == "" {
			return fmt.Errorf("source_key_required")
		}
		if !commitRef.MatchString(src.Key) {
			return fmt.Errorf("invalid_source_key")
		}
		if keys[src.Key] {
			return fmt.Errorf("duplicate_source_key")
		}
		keys[src.Key] = true
	}
	used := map[string]bool{}
	for i, op := range in.Operations {
		fail := func(err error) error { return &commitOperationError{i, err} }
		if len(op.SourceRefs) == 0 {
			return fail(fmt.Errorf("source_refs_required"))
		}
		var doc any
		if op.Action == "create" {
			if err := json.Unmarshal(op.Document, &doc); err != nil {
				return fail(fmt.Errorf("invalid_document"))
			}
		}
		seenRef := map[string]bool{}
		for _, ref := range op.SourceRefs {
			if !keys[ref.Source] {
				return fail(fmt.Errorf("unknown_source_ref"))
			}
			if seenRef[ref.Source] {
				return fail(fmt.Errorf("duplicate_source_ref"))
			}
			seenRef[ref.Source] = true
			used[ref.Source] = true
			seenPointer := map[string]bool{}
			for _, ptr := range ref.Pointers {
				parts, err := pointerParts(ptr)
				if err != nil || seenPointer[ptr] {
					return fail(fmt.Errorf("invalid_source_pointer"))
				}
				seenPointer[ptr] = true
				switch op.Action {
				case "create":
					if !documentHasPointer(doc, parts) {
						return fail(fmt.Errorf("source_pointer_not_in_document"))
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
						return fail(fmt.Errorf("source_pointer_not_patched"))
					}
				}
			}
		}
	}
	for _, src := range in.Sources {
		if !used[src.Key] {
			return fmt.Errorf("source_unused")
		}
	}
	return nil
}

// operationSources 返回写入该操作修订的来源：只取被引用的来源并带上 pointers
// （validateCommitSourceRefs 已保证每个操作都有 source_refs）。
func operationSources(in CatalogCommit, op CommitOperation) []Source {
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
