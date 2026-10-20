package catalog

import (
	"context"
)

// Both parents participate in ancestry. The selected common ancestor is an
// actual retained commit; no historical parent or virtual snapshot is invented.
func versionMergeBase(ctx context.Context, q queryer, left, right string) (*string, error) {
	if left == "" || right == "" {
		return nil, nil
	}
	var base *string
	err := q.QueryRowContext(ctx, `WITH RECURSIVE a(id) AS(
 SELECT id FROM catalog.commits WHERE id=$1
 UNION SELECT p.id FROM a JOIN catalog.commits c ON c.id=a.id CROSS JOIN LATERAL(VALUES(c.parent_id),(c.merge_parent_id)) p(id) WHERE p.id IS NOT NULL
 ),b(id) AS(
 SELECT id FROM catalog.commits WHERE id=$2
 UNION SELECT p.id FROM b JOIN catalog.commits c ON c.id=b.id CROSS JOIN LATERAL(VALUES(c.parent_id),(c.merge_parent_id)) p(id) WHERE p.id IS NOT NULL
 ),common AS(SELECT id FROM a INTERSECT SELECT id FROM b)
 SELECT(SELECT k.id::text FROM common k JOIN catalog.commits c ON c.id=k.id
 WHERE NOT EXISTS(SELECT 1 FROM common d JOIN catalog.commits child ON child.id=d.id WHERE child.parent_id=k.id OR child.merge_parent_id=k.id)
 ORDER BY c.created_at DESC,k.id LIMIT 1)`, left, right).Scan(&base)
	return base, err
}

func versionFirstParentContains(ctx context.Context, q queryer, head, target string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, `WITH RECURSIVE chain AS(SELECT id,parent_id,ARRAY[id] AS visited FROM catalog.commits WHERE id=$1 UNION ALL SELECT c.id,c.parent_id,a.visited||c.id FROM chain a JOIN catalog.commits c ON c.id=a.parent_id WHERE NOT c.id=ANY(a.visited)) SELECT EXISTS(SELECT 1 FROM chain WHERE id=$2)`, head, target).Scan(&found)
	return found, err
}

// Traverse both parents once and return ancestor-first branch members. Stop at
// the official catalog boundary; main checkpoints are not pending branch edits.
func versionBranchCommits(ctx context.Context, q queryer, tip string, boundary *string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE reachable(id) AS(
 SELECT id FROM catalog.commits WHERE id=$1 AND snapshot_scope<>'catalog' AND ($2::uuid IS NULL OR id<>$2)
 UNION SELECT p.id FROM reachable r JOIN catalog.commits c ON c.id=r.id CROSS JOIN LATERAL(VALUES(c.parent_id),(c.merge_parent_id)) p(id) JOIN catalog.commits n ON n.id=p.id
 WHERE n.snapshot_scope<>'catalog' AND ($2::uuid IS NULL OR n.id<>$2))
 SELECT c.id::text,c.parent_id::text,c.merge_parent_id::text FROM reachable r JOIN catalog.commits c ON c.id=r.id`, tip, boundary)
	if err != nil {
		return nil, err
	}
	type node struct{ parents [2]*string }
	nodes := map[string]node{}
	for rows.Next() {
		var id string
		var n node
		if err = rows.Scan(&id, &n.parents[0], &n.parents[1]); err != nil {
			rows.Close()
			return nil, err
		}
		nodes[id] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		n, ok := nodes[id]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		for _, p := range n.parents {
			if p != nil {
				visit(*p)
			}
		}
		out = append(out, id)
	}
	visit(tip)
	return out, nil
}
