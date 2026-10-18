-- Working copies and immutable commits replace the single-submission review
-- queue. Only a merge materializes facts; there is no global catalog head.
-- Hold historical writers while their frozen proposals are migrated.
LOCK TABLE catalog.commits, catalog.revisions IN ACCESS EXCLUSIVE MODE;

CREATE TABLE catalog.workspaces (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL CHECK (owner_id<>''),
    name text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    head_commit_id uuid,
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','archived')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workspaces_owner_updated_idx ON catalog.workspaces(owner_id,updated_at DESC,id);

CREATE TABLE catalog.workspace_members (
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id) ON DELETE CASCADE,
    user_id text NOT NULL CHECK (user_id<>''),
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id,user_id)
);
CREATE INDEX workspace_members_user_idx ON catalog.workspace_members(user_id,workspace_id);

-- SQL NULL means that an object does not exist (including a staged deletion).
-- JSON null is deliberately not a second representation of that state.
CREATE TABLE catalog.workspace_objects (
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id) ON DELETE CASCADE,
    target text NOT NULL CHECK (target IN ('entity','relation')),
    target_id uuid NOT NULL,
    base_version bigint NOT NULL DEFAULT 0 CHECK (base_version>=0),
    base_document jsonb CHECK (base_document IS NULL OR jsonb_typeof(base_document)='object'),
    head_document jsonb CHECK (head_document IS NULL OR jsonb_typeof(head_document)='object'),
    document jsonb CHECK (document IS NULL OR jsonb_typeof(document)='object'),
    staged_authors jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(staged_authors)='object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id,target,target_id)
);

ALTER TABLE catalog.commits
    ADD COLUMN workspace_id uuid REFERENCES catalog.workspaces(id),
    ADD COLUMN parent_id uuid REFERENCES catalog.commits(id),
    ADD COLUMN message text NOT NULL DEFAULT '',
    ADD COLUMN merged_at timestamptz,
    ADD COLUMN merged_by text,
    ADD COLUMN pull_request_id uuid,
    ADD COLUMN historical_review jsonb CHECK (historical_review IS NULL OR jsonb_typeof(historical_review)='object'),
    ADD COLUMN kind text NOT NULL DEFAULT 'edit' CHECK (kind IN ('edit','rebase'));
ALTER TABLE catalog.workspaces ADD CONSTRAINT workspaces_head_commit_fk
    FOREIGN KEY (head_commit_id) REFERENCES catalog.commits(id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX commits_workspace_created_idx ON catalog.commits(workspace_id,created_at,id);
CREATE INDEX commits_merged_idx ON catalog.commits(merged_at DESC,id) WHERE merged_at IS NOT NULL;

CREATE TABLE catalog.commit_objects (
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    target text NOT NULL CHECK (target IN ('entity','relation')),
    target_id uuid NOT NULL,
    base_version bigint NOT NULL DEFAULT 0 CHECK (base_version>=0),
    base_document jsonb CHECK (base_document IS NULL OR jsonb_typeof(base_document)='object'),
    before_document jsonb CHECK (before_document IS NULL OR jsonb_typeof(before_document)='object'),
    after_document jsonb CHECK (after_document IS NULL OR jsonb_typeof(after_document)='object'),
    PRIMARY KEY (commit_id,target,target_id)
);
CREATE INDEX commit_objects_target_idx ON catalog.commit_objects(target,target_id,commit_id);

CREATE TABLE catalog.commit_authors (
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    user_id text NOT NULL CHECK (user_id<>''),
    name text NOT NULL DEFAULT '',
    PRIMARY KEY (commit_id,user_id)
);
CREATE INDEX commit_authors_user_idx ON catalog.commit_authors(user_id,commit_id);
INSERT INTO catalog.commit_authors(commit_id,user_id,name)
SELECT id,actor_id,COALESCE(author_snapshot->>'username','') FROM catalog.commits WHERE actor_id<>'';

CREATE TABLE catalog.pull_requests (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id),
    author_id text NOT NULL CHECK (author_id<>''),
    title text NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','merged','closed')),
    source_head_id uuid NOT NULL REFERENCES catalog.commits(id),
    source_base_id uuid REFERENCES catalog.commits(id),
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    merged_at timestamptz,
    merged_by text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX pull_requests_workspace_open_idx ON catalog.pull_requests(workspace_id) WHERE status='open';
CREATE INDEX pull_requests_status_updated_idx ON catalog.pull_requests(status,updated_at DESC,id);
CREATE INDEX pull_requests_author_updated_idx ON catalog.pull_requests(author_id,updated_at DESC,id);
ALTER TABLE catalog.commits ADD CONSTRAINT commits_pull_request_fk
    FOREIGN KEY (pull_request_id) REFERENCES catalog.pull_requests(id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE catalog.revision_commits (
    revision_id bigint NOT NULL REFERENCES catalog.revisions(id) ON DELETE RESTRICT,
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    PRIMARY KEY (revision_id,commit_id)
);
CREATE INDEX revision_commits_commit_idx ON catalog.revision_commits(commit_id,revision_id);
INSERT INTO catalog.revision_commits(revision_id,commit_id)
SELECT id,commit_id FROM catalog.revisions WHERE commit_id IS NOT NULL;

-- The immutable receipt, original request/hash and author snapshot stay exactly
-- as recorded. Do not recreate a missing request or infer a historical parent.
UPDATE catalog.commits c SET
    historical_review=jsonb_build_object(
        'review_status',c.review_status,'review_decision',c.review_decision,
        'reviewed_by',c.reviewed_by,'reviewed_at',c.reviewed_at,'review_note',c.review_note),
    message=COALESCE(c.request->>'edit_note',(
        SELECT r.edit_note FROM catalog.revisions r
        WHERE r.commit_id=c.id ORDER BY r.id LIMIT 1),''),
    merged_at=CASE WHEN c.review_decision IN ('approved','exempt')
        THEN COALESCE(c.reviewed_at,c.created_at) END,
    merged_by=CASE WHEN c.review_decision IN ('approved','exempt')
        THEN COALESCE(c.reviewed_by,c.actor_id) END;

-- A commit may have touched the same object more than once. Its object entry
-- preserves the first before-state and final after-state in operation order.
WITH previews AS (
    SELECT c.id AS commit_id,p.value->>'target' AS target,
        (p.value->>'id')::uuid AS target_id,p.ordinality,
        NULLIF(p.value->'before','null'::jsonb) AS before_document,
        NULLIF(p.value->'after','null'::jsonb) AS after_document
    FROM catalog.commits c
    CROSS JOIN LATERAL jsonb_array_elements(CASE
        WHEN jsonb_typeof(c.result->'preview')='array' THEN c.result->'preview'
        ELSE '[]'::jsonb END) WITH ORDINALITY AS p(value,ordinality)
), first_objects AS (
    SELECT DISTINCT ON (commit_id,target,target_id)
        commit_id,target,target_id,before_document
    FROM previews ORDER BY commit_id,target,target_id,ordinality
), last_objects AS (
    SELECT DISTINCT ON (commit_id,target,target_id)
        commit_id,target,target_id,after_document
    FROM previews ORDER BY commit_id,target,target_id,ordinality DESC
)
INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,base_document,before_document,after_document)
SELECT f.commit_id,f.target,f.target_id,COALESCE((f.before_document->>'version')::bigint,0),
    f.before_document,f.before_document,l.after_document
FROM first_objects f JOIN last_objects l USING(commit_id,target,target_id)
WHERE NOT EXISTS (
    SELECT 1 FROM catalog.revisions r JOIN catalog.commits c ON c.id=r.commit_id
    WHERE c.id=f.commit_id AND c.merged_at IS NOT NULL AND r.target_id=f.target_id::text
      AND ((f.target='entity' AND r.snapshot->>'kind' IN ('agent','collection','work','content_unit','expression','release','medium','track'))
        OR (f.target='relation' AND r.snapshot ? 'source_id' AND r.snapshot ? 'target_id' AND r.snapshot ? 'type'))
);

-- Actual applied revisions take precedence over a frozen review preview: an
-- approved proposal may have been merged with newer main-catalog facts.
-- Retention may already have removed a request, and early receipts did not
-- contain previews. Recover only the facts actually retained by revisions.
-- Non-object revisions (definition/rate-policy summaries) are not catalog
-- entities or relations and remain available in their original history.
WITH object_revisions AS (
    SELECT r.*,CASE
        WHEN r.snapshot->>'kind' IN ('agent','collection','work','content_unit','expression','release','medium','track') THEN 'entity'
        WHEN r.snapshot ? 'source_id' AND r.snapshot ? 'target_id' AND r.snapshot ? 'type' THEN 'relation'
        END AS target
    FROM catalog.revisions r JOIN catalog.commits c ON c.id=r.commit_id
    WHERE c.merged_at IS NOT NULL
), first_objects AS (
    SELECT DISTINCT ON (commit_id,target,target_id) * FROM object_revisions
    WHERE target IS NOT NULL ORDER BY commit_id,target,target_id,id
), last_objects AS (
    SELECT DISTINCT ON (commit_id,target,target_id) * FROM object_revisions
    WHERE target IS NOT NULL ORDER BY commit_id,target,target_id,id DESC
)
INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,base_document,before_document,after_document)
SELECT f.commit_id,f.target,f.target_id::uuid,COALESCE(prior.version,f.version-1),
    CASE WHEN prior.id IS NOT NULL THEN
        CASE WHEN f.target='relation' AND (prior.snapshot->>'version')::bigint=prior.version-1
            THEN NULL ELSE prior.snapshot END
        WHEN f.target='relation' AND (f.snapshot->>'version')::bigint=f.version-1 THEN f.snapshot
        WHEN f.version=1 THEN NULL
        ELSE frozen.document END,
    CASE WHEN prior.id IS NOT NULL THEN
        CASE WHEN f.target='relation' AND (prior.snapshot->>'version')::bigint=prior.version-1
            THEN NULL ELSE prior.snapshot END
        WHEN f.target='relation' AND (f.snapshot->>'version')::bigint=f.version-1 THEN f.snapshot
        WHEN f.version=1 THEN NULL
        ELSE frozen.document END,
    CASE WHEN l.target='relation' AND (l.snapshot->>'version')::bigint=l.version-1
        THEN NULL ELSE l.snapshot END
FROM first_objects f
JOIN last_objects l USING(commit_id,target,target_id)
LEFT JOIN LATERAL (
    SELECT r.id,r.version,r.snapshot FROM catalog.revisions r
    WHERE r.target_id=f.target_id AND r.id<f.id AND r.version=f.version-1
    ORDER BY r.version DESC,r.id DESC LIMIT 1
) prior ON true
LEFT JOIN LATERAL (
    SELECT NULLIF(p.value->'before','null'::jsonb) AS document
    FROM catalog.commits c
    CROSS JOIN LATERAL jsonb_array_elements(CASE
        WHEN jsonb_typeof(c.result->'preview')='array' THEN c.result->'preview'
        ELSE '[]'::jsonb END) WITH ORDINALITY AS p(value,ordinality)
    WHERE c.id=f.commit_id AND p.value->>'target'=f.target AND p.value->>'id'=f.target_id
    ORDER BY p.ordinality LIMIT 1
) frozen ON true
-- Missing retained history does not prove that an object was a creation.
-- A matching frozen before-version or a deletion snapshot can prove the base;
-- otherwise retain the original revision without inventing a commit object.
WHERE prior.id IS NOT NULL OR f.version=1
    OR (f.target='relation' AND (f.snapshot->>'version')::bigint=f.version-1)
    OR (frozen.document->>'version')::bigint=f.version-1
ON CONFLICT(commit_id,target,target_id) DO UPDATE SET
    base_version=EXCLUDED.base_version,
    base_document=EXCLUDED.base_document,
    before_document=EXCLUDED.before_document,
    after_document=EXCLUDED.after_document;

-- Each unmerged historical proposal gets its own working copy and PR. The
-- proposal ID remains the original immutable commit ID, not a synthetic edit.
CREATE TEMP TABLE migrated_proposals ON COMMIT DROP AS
SELECT c.id AS commit_id,gen_random_uuid() AS workspace_id,gen_random_uuid() AS pull_request_id,
    c.actor_id,c.message,c.created_at,COALESCE(c.reviewed_at,c.created_at) AS updated_at,
    CASE WHEN c.review_decision='pending' THEN 'open' ELSE 'closed' END AS status
FROM catalog.commits c WHERE c.review_decision IN ('pending','rejected');

INSERT INTO catalog.workspaces(id,owner_id,name,head_commit_id,created_at,updated_at)
SELECT workspace_id,actor_id,message,commit_id,created_at,updated_at FROM migrated_proposals;
INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_version,base_document,head_document,document,updated_at)
SELECT p.workspace_id,o.target,o.target_id,o.base_version,o.before_document,o.after_document,o.after_document,p.updated_at
FROM migrated_proposals p JOIN catalog.commit_objects o USING(commit_id);
INSERT INTO catalog.pull_requests(id,workspace_id,author_id,title,status,source_head_id,created_at,updated_at)
SELECT pull_request_id,workspace_id,actor_id,message,status,commit_id,created_at,updated_at FROM migrated_proposals;
UPDATE catalog.commits c SET workspace_id=p.workspace_id,pull_request_id=p.pull_request_id
FROM migrated_proposals p WHERE c.id=p.commit_id;

-- New runtime code uses workspaces/PRs exclusively. Old review fields are
-- archived verbatim even if they differ from their historical JSON receipt;
-- historical_review is evidence and is never read to execute a merge.
-- Historical workspace/PR backfills queued deferred FK checks. Flush them
-- before changing the same table's structure, and fail on any invalid link.
SET CONSTRAINTS ALL IMMEDIATE;
DROP INDEX catalog.commits_request_retention_idx;
ALTER TABLE catalog.commits
    DROP COLUMN review_status,
    DROP COLUMN review_decision,
    DROP COLUMN reviewed_by,
    DROP COLUMN reviewed_at,
    DROP COLUMN review_note;
UPDATE catalog.schema_contract SET version=35 WHERE singleton=true;
