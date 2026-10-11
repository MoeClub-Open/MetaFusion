#!/usr/bin/env python3
"""MetaFusion 目录数据只读自检。

用途：把编目过程中反复靠人工 SQL/目视发现的问题固化成一条命令，便于定期跑或发布前核对。
只读：仅调用 GET /api/catalog/*，不写任何数据；对未登录可见性不足的项会如实标注"未检查"。

用法：
  python scripts/check_data.py                       # 默认查 https://findverse.cc
  BASE=https://example.org python scripts/check_data.py
  TOKEN=<bearer> python scripts/check_data.py        # 带上令牌可把草稿一并纳入

检查项：
  1. 发布态与翻译：published 但没有任何翻译记录的实体；
  2. 结构归属：definitions.structure 声明为 required 的结构字段为空；声明了 target_kinds 的字段指向了不匹配的层级；
  3. 图片：按层级的期望（work/release/agent/collection 至少一张；medium/track/expression 允许无图）统计缺图；
  4. 关系：端点不可见（指向已删/未发布）的边、同一 (type, source, target) 的重复边；
  5. 名称四语：缺语种、某语种仍逐字等于英文（占位）。覆盖 definitions 全部名称
     （类型/字段及嵌套子字段与单位/词表与词项/关系正反名与分组名/模板与分区/场景方案/kinds）
     以及货架、外部权威库；判据与后端写路径同一口径（zh-CN/zh-TW/en-US + ja|ja-JP）。
  6. ISBN / EAN 校验位：isbn 属性与 openbd / isbndb 编号检查 ISBN-10/13；
     标准长度 barcode 与 ean_jan 编号检查 GTIN-8/12/13/14，非法值计 P1。
退出码：0 = 无 P0/P1；1 = 存在 P0/P1（名称问题计 P2，只报告不拦门）。
"""

import json
import os
import sys
import urllib.parse
import urllib.request
from collections import Counter, defaultdict

BASE = os.environ.get("BASE", "https://findverse.cc").rstrip("/")
TOKEN = os.environ.get("TOKEN", "").strip()


def get(path, params=None):
    url = BASE + path
    if params:
        url += "?" + urllib.parse.urlencode(params, doseq=True)
    req = urllib.request.Request(url)
    # 网关/CDN 会拦默认 UA（Python-urllib → 403），带上可辨识的客户端标识。
    req.add_header("User-Agent", "MetaFusion-DataCheck/1.0 (+https://findverse.cc)")
    req.add_header("Accept", "application/json")
    if TOKEN:
        req.add_header("Authorization", "Bearer " + TOKEN)
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def all_entities(kind=None, limit=50):
    # 服务端会夹住 limit（实测传 200 只回 50），所以按返回条数判断是否到底，不假设页大小。
    items, offset, page = [], 0, limit
    while True:
        params = {"limit": limit, "offset": offset}
        if kind:
            params["kind"] = kind
        data = get("/api/catalog/entities", params)
        batch = data.get("items") or []
        items.extend(batch)
        if len(batch) < page:
            return items
        offset += len(batch)


def compact_book_identifier(value):
    """只去除 ISBN 的空白/连字符；不从任意文字或 URL 中提取数字。"""
    if not isinstance(value, str):
        return None
    value = value.strip()
    if any(ch not in "0123456789Xx- \t\r\n" for ch in value):
        return None
    return "".join(ch.upper() for ch in value if ch not in "- \t\r\n")


def valid_gtin(compact):
    """EAN-8、UPC-A、EAN-13/JAN 与 GTIN-14 使用同一加权校验位。"""
    if not isinstance(compact, str) or len(compact) not in (8, 12, 13, 14):
        return False
    if any(ch not in "0123456789" for ch in compact):
        return False
    total = sum(int(ch) * (3 if index % 2 == 0 else 1)
                for index, ch in enumerate(reversed(compact[:-1])))
    return int(compact[-1]) == (10 - total % 10) % 10


def valid_isbn(compact):
    if not isinstance(compact, str):
        return False
    if len(compact) == 13:
        return compact.startswith(("978", "979")) and valid_gtin(compact)
    if len(compact) != 10 or any(ch not in "0123456789" for ch in compact[:9]):
        return False
    if compact[-1] not in "0123456789X":
        return False
    total = sum(int(ch) * (10 - index) for index, ch in enumerate(compact[:9]))
    total += 10 if compact[-1] == "X" else int(compact[-1])
    return total % 11 == 0


def check_product_identifiers(entity, problems):
    """已知 ISBN/GTIN 声明查校验位，不猜测任意动态字段或专用文本条码的制式。"""
    ident = entity.get("id", "?")
    attributes = entity.get("attributes") or {}
    isbn = attributes.get("isbn")
    if isbn not in (None, "") and not valid_isbn(compact_book_identifier(isbn)):
        problems.append(("P1", "invalid_isbn", ident, "attributes.isbn=%s" % str(isbn)))
    barcode = attributes.get("barcode")
    compact = compact_book_identifier(barcode)
    if compact is not None and len(compact) in (8, 10, 12, 13, 14):
        valid = valid_isbn(compact) if len(compact) == 10 else valid_gtin(compact)
        if not valid:
            problems.append(("P1", "invalid_barcode", ident, "attributes.barcode=%s" % str(barcode)))
    external_ids = entity.get("external_ids") or {}
    for code in ("openbd", "isbndb"):
        value = external_ids.get(code)
        if value not in (None, "") and not valid_isbn(compact_book_identifier(value)):
            problems.append(("P1", "invalid_isbn", ident, "external_ids.%s=%s" % (code, value)))
    value = external_ids.get("ean_jan")
    if value not in (None, "") and not valid_gtin(compact_book_identifier(value)):
        problems.append(("P1", "invalid_barcode", ident, "external_ids.ean_jan=%s" % str(value)))


# ── 名称四语检查（与后端写路径同一口径，见 backend/internal/catalog/validation.go）──
# 后端对启用中的定义/货架/外部库强制四语（缺则 four_locale_names_required）；
# 这里把线上载荷逐条摊平，把「缺语种」与「仍逐字等于英文」都报出来。
# 专有名词（MusicBrainz / ISBNdb / CD）在多语种里本就同形，命中占位只作提示，不进闸门。
REQUIRED_LOCALES = ("zh-CN", "zh-TW", "en-US")


def check_names(label, names, problems):
    """检查一条名称：缺语种与英文占位各报一条 P2。"""
    if not isinstance(names, dict) or not names:
        return
    missing = [loc for loc in REQUIRED_LOCALES if not str(names.get(loc) or "").strip()]
    if not str(names.get("ja") or "").strip() and not str(names.get("ja-JP") or "").strip():
        missing.append("ja-JP")
    if missing:
        problems.append(("P2", "name_missing_locale", label, ",".join(missing)))
    en = names.get("en-US")
    for loc in ("ja-JP", "zh-TW"):
        if names.get(loc) and en and names[loc] == en:
            problems.append(("P2", "name_placeholder", label, loc))


def check_field_names(label, field, problems):
    """字段名称递归检查：字段名、单位名、列表项、嵌套子字段。"""
    if not isinstance(field, dict):
        return
    check_names(label + ".names", field.get("names"), problems)
    check_names(label + ".unit", field.get("unit"), problems)
    if isinstance(field.get("items"), dict):
        check_field_names(label + ".items", field["items"], problems)
    for code, child in (field.get("fields") or {}).items():
        check_field_names(label + "." + code, child, problems)


def check_definition_names(defs, problems):
    """检查 definitions 载荷里的全部名称（含 kinds 四语骨架名）。"""
    doc = defs.get("document") or defs
    for code, v in (defs.get("kinds") or {}).items():
        check_names("kinds." + code, (v or {}).get("names"), problems)
    for code, v in (doc.get("types") or {}).items():
        check_names("types." + code, (v or {}).get("names"), problems)
    for code, v in (doc.get("fields") or {}).items():
        check_field_names("fields." + code, v, problems)
    for code, v in (doc.get("vocabularies") or {}).items():
        v = v or {}
        check_names("vocabularies." + code, v.get("names"), problems)
        for term, tv in (v.get("terms") or {}).items():
            check_names("vocabularies.%s.terms.%s" % (code, term), (tv or {}).get("names"), problems)
    for code, v in (doc.get("relations") or {}).items():
        v = v or {}
        check_names("relations." + code, v.get("names"), problems)
        check_names("relations." + code + ".reverse_names", v.get("reverse_names"), problems)
        check_names("relations." + code + ".group_names", v.get("group_names"), problems)
    for code, v in (doc.get("templates") or {}).items():
        v = v or {}
        check_names("templates." + code, v.get("names"), problems)
        for i, section in enumerate(v.get("sections") or []):
            check_names("templates.%s.sections[%d]" % (code, i), (section or {}).get("names"), problems)
    for code, v in (doc.get("schemes") or {}).items():
        check_names("schemes." + code, (v or {}).get("names"), problems)


def check_list_names(path, key, problems):
    """检查一个「列表型多语名称接口」（货架 / 外部权威库）；取不到就如实标注未检查。"""
    try:
        data = get(path)
    except Exception as exc:  # 只读自检不应因一个可选端点整体失败
        problems.append(("P2", "name_check_skipped", path, str(exc)[:80]))
        return
    for item in (data.get("items") or data or []):
        if not isinstance(item, dict):
            continue
        ident = item.get("slug") or item.get("code") or "?"
        check_names("%s.%s" % (key, ident), item.get("names"), problems)


def main():
    problems = []
    stats = Counter()
    defs = get("/api/catalog/definitions")
    doc = defs.get("document") or defs
    relations = doc.get("relations") or {}
    structure = doc.get("structure") or {}

    entities = all_entities()
    stats["entities"] = len(entities)
    by_id = {e["id"]: e for e in entities}

    # 1. 发布态与翻译
    for e in entities:
        stats["kind:" + str(e.get("kind"))] += 1
        if e.get("status") == "published" and not (e.get("translations") or {}):
            problems.append(("P1", "published_without_translation", e["id"], e.get("title", "")))
        check_product_identifiers(e, problems)
    # 2. 结构归属（以 definitions.structure 为准，不写死层级）
    for e in entities:
        rule = structure.get(str(e.get("kind"))) or {}
        for f in rule.get("fields") or []:
            code = f.get("code")
            val = e.get(code)
            if f.get("required") and not val:
                problems.append(("P1", "structure_required_missing", e["id"], "%s.%s" % (e.get("kind"), code)))
            targets = f.get("target_kinds") or []
            if val and targets:
                tgt = by_id.get(val)
                if tgt is None:
                    problems.append(("P0", "structure_target_not_visible", e["id"], "%s.%s -> %s" % (e.get("kind"), code, val)))
                elif tgt.get("kind") not in targets:
                    problems.append(("P1", "structure_target_kind_mismatch", e["id"], "%s.%s -> %s(%s)" % (e.get("kind"), code, tgt.get("title"), tgt.get("kind"))))
    # 3. 图片：按类型只做"提示"，不进 P0/P1 闸门。
    #    原因：medium/track/expression 本就不需要图；song 这类细分类型官方也没有独立图，
    #    一刀切报 P2 会变成噪音，反而掩盖真问题。这里按"层级/类型"聚合后在报告末尾提示。
    no_pic_by_type = Counter()
    for e in entities:
        if e.get("pictures"):
            continue
        labels = e.get("types") or [str(e.get("kind"))]
        for label in labels:
            no_pic_by_type["%s/%s" % (e.get("kind"), label)] += 1
    # 4. 关系：重复边与端点不可见
    seen = defaultdict(int)
    checked = 0
    for e in entities:
        rel = get("/api/catalog/entities/%s/relations" % e["id"])
        items = rel.get("items") or []
        # 关系响应自带实体映射：端点可见性以它为准，不依赖全局列表是否分页完整。
        local_entities = rel.get("entities") or {}
        for r in items:
            # 一条边会在两端各出现一次：只在 source 端计数，避免把同一条边当成重复。
            if r.get("source_id") == e.get("id"):
                key = (r.get("type"), r.get("source_id"), r.get("target_id"))
                seen[key] += 1
                checked += 1
            for field in ("source_id", "target_id"):
                rid = r.get(field)
                if rid and rid not in local_entities and rid not in by_id:
                    problems.append(("P1", "relation_endpoint_not_visible", e["id"], "%s.%s -> %s" % (r.get("type"), field, rid)))
            if r.get("type") not in relations:
                problems.append(("P1", "relation_code_unknown", e["id"], str(r.get("type"))))
    stats["relation_edges_checked"] = checked
    for key, n in seen.items():
        if n > 1:
            problems.append(("P1", "duplicate_relation", key[0], "%s -> %s x%d" % (key[1], key[2], n)))
    # 5. 覆盖度自检：该有却没有的关系与从属（"缺关系/缺翻译/缺图片/层级异常"里的"缺关系"）
    #    硬闸门只放"规范要求必须成立"的两条：乐队必须有成员、发行版必须有载体；
    #    其余按提示列出（例如影像作品的篇目数、载体曲目数），因为它们有正当的例外（剧场版单篇、Live 盘无分轨）。
    import collections as _c
    rel_by_src = _c.Counter()
    rel_by_pair = _c.Counter()
    rel_types = _c.Counter()
    rel_cache = {}
    for e in entities:
        if e.get("kind") not in ("agent", "work", "release", "collection", "content_unit", "expression", "medium", "track"):
            continue
        rel = get("/api/catalog/entities/%s/relations" % e["id"]) or {}
        rel_cache[e["id"]] = rel
        for r in (rel.get("items") or []):
            rel_types[r.get("type")] += 1
            rel_by_pair[(r.get("type"), r.get("source_id"), r.get("target_id"))] += 1
            rel_by_src[(r.get("type"), r.get("target_id"))] += 1
    # 5.1 乐队必须有成员（member_of 指向它）
    for e in entities:
        if e.get("kind") != "agent":
            continue
        if "group" not in (e.get("types") or []):
            continue
        if rel_by_src[("member_of", e["id"])] == 0:
            problems.append(("P1", "group_without_members", e["id"], e.get("title", "")))
    # 5.2 发行版必须有载体（medium 挂在它下面）
    for e in entities:
        if e.get("kind") != "release":
            continue
        meds = get("/api/catalog/entities", {"release_id": e["id"], "limit": 50}) or {}
        if not (meds.get("items") or []):
            problems.append(("P1", "release_without_medium", e["id"], (e.get("attributes") or {}).get("catalog_number") or e.get("title", "")))
    # 5.3 提示项：影像作品无篇目、载体无曲目、作品不在任何集合里
    hints = _c.Counter()
    for e in entities:
        types = e.get("types") or []
        if e.get("kind") == "work" and any(t in types for t in ("animation",)):
            cus = get("/api/catalog/entities", {"work_id": e["id"], "kind": "content_unit", "limit": 1}) or {}
            if not (cus.get("total") or 0):
                hints["animation_without_episodes"] += 1
        if e.get("kind") == "medium":
            trs = get("/api/catalog/entities", {"medium_id": e["id"], "limit": 1}) or {}
            if not (trs.get("total") or 0):
                hints["medium_without_tracks"] += 1
        if e.get("kind") == "work" and not (rel_cache.get(e["id"]) or {}).get("items"):
            hints["work_without_relations"] += 1
    if hints:
        print("\n覆盖度提示（不计入 P0/P1；有正当例外，如剧场版单篇、Live 盘无分轨）:")
        for name, n in hints.most_common():
            print("  %-30s %d" % (name, n))
    print("关系类型分布: " + ", ".join("%s=%d" % (k, v) for k, v in rel_types.most_common(8)))
    # 5. 名称四语：与写路径同一口径（缺语种 / 仍等于英文占位），含货架与外部权威库。
    check_definition_names(defs, problems)
    check_list_names("/api/catalog/shelves", "shelves", problems)
    check_list_names("/api/catalog/external-databases", "external_databases", problems)

    counts = Counter(p[0] for p in problems)
    print("自检目标: %s" % BASE)
    print("实体 %d（含草稿 %s），涉及关系边约 %d" % (stats["entities"], "是" if TOKEN else "否", stats["relation_edges_checked"]))
    print("问题分级: " + ", ".join("%s=%d" % (k, counts[k]) for k in ("P0", "P1", "P2") if counts[k]) or "无")
    by_kind = Counter(p[1] for p in problems)
    for name, n in by_kind.most_common():
        print("  %-34s %d" % (name, n))
    if no_pic_by_type:
        print("\n缺图提示（不计入 P0/P1；官方无图或本层级不需要图的属正常）:")
        for name, n in no_pic_by_type.most_common(12):
            print("  %-28s %d" % (name, n))
    show = [p for p in problems if p[0] in ("P0", "P1")]
    if show:
        print("\nP0/P1 明细（最多 40 条）：")
        for lvl, name, ident, detail in show[:40]:
            print("  [%s] %-32s %s  %s" % (lvl, name, str(ident)[:12], detail[:80]))
    return 1 if counts["P0"] or counts["P1"] else 0


if __name__ == "__main__":
    sys.exit(main())
