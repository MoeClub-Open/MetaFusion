-- Branch membership is independent of the workspace in which a commit was
-- authored. Original commits and requests are never rewritten or copied.
CREATE TABLE catalog.workspace_commits (
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id),
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    position bigint NOT NULL,
    active boolean NOT NULL DEFAULT true,
    integrated boolean NOT NULL DEFAULT false,
    PRIMARY KEY (workspace_id,commit_id),
    UNIQUE (workspace_id,position)
);
CREATE INDEX workspace_commits_commit_idx ON catalog.workspace_commits(commit_id,workspace_id);
INSERT INTO catalog.workspace_commits(workspace_id,commit_id,position,integrated)
SELECT workspace_id,id,row_number() OVER (PARTITION BY workspace_id ORDER BY created_at,id),merged_at IS NOT NULL
FROM catalog.commits WHERE workspace_id IS NOT NULL;

ALTER TABLE catalog.workspaces ADD COLUMN creation_hash text NOT NULL DEFAULT '';
ALTER TABLE catalog.workspaces ADD COLUMN base_commit_id uuid REFERENCES catalog.commits(id);
ALTER TABLE catalog.commits ADD COLUMN merge_parent_id uuid REFERENCES catalog.commits(id);
ALTER TABLE catalog.commits ADD COLUMN snapshot_scope text NOT NULL DEFAULT 'known_records'
    CHECK (snapshot_scope IN ('known_records','tracked_records','catalog'));

CREATE TABLE catalog.commit_snapshots (
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    target text NOT NULL CHECK (target IN ('entity','relation')),
    target_id uuid NOT NULL,
    base_version bigint NOT NULL,
    base_document jsonb,
    document jsonb,
    PRIMARY KEY (commit_id,target,target_id)
);
-- Backfill only facts actually retained in historical commits. There is no
-- invented historical checkout set, parent chain, author or whole-site tree.
WITH RECURSIVE ancestry AS (
    SELECT id AS tip,id,parent_id,0 AS depth,ARRAY[id] AS visited FROM catalog.commits
    UNION ALL
    SELECT a.tip,c.id,c.parent_id,a.depth+1,a.visited||c.id
    FROM ancestry a JOIN catalog.commits c ON c.id=a.parent_id
    WHERE NOT c.id=ANY(a.visited)
), snapshots AS (
    SELECT DISTINCT ON (a.tip,o.target,o.target_id)
        a.tip,o.target,o.target_id,o.base_version,o.base_document,o.after_document
    FROM ancestry a JOIN catalog.commit_objects o ON o.commit_id=a.id
    ORDER BY a.tip,o.target,o.target_id,a.depth
)
INSERT INTO catalog.commit_snapshots SELECT * FROM snapshots;

CREATE TABLE catalog.pull_request_commits (
    pull_request_id uuid NOT NULL REFERENCES catalog.pull_requests(id),
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    position bigint NOT NULL,
    PRIMARY KEY (pull_request_id,commit_id),
    UNIQUE (pull_request_id,position)
);
WITH RECURSIVE submitted AS (
    SELECT p.id AS pr,p.workspace_id,p.source_base_id,c.id,c.parent_id,0 AS depth,ARRAY[c.id] AS visited
    FROM catalog.pull_requests p JOIN catalog.commits c ON c.id=p.source_head_id
    WHERE p.source_base_id IS NULL OR c.id<>p.source_base_id
    UNION ALL
    SELECT s.pr,s.workspace_id,s.source_base_id,c.id,c.parent_id,s.depth+1,s.visited||c.id
    FROM submitted s JOIN catalog.commits c ON c.id=s.parent_id
    WHERE c.workspace_id=s.workspace_id AND (s.source_base_id IS NULL OR c.id<>s.source_base_id)
        AND NOT c.id=ANY(s.visited)
)
INSERT INTO catalog.pull_request_commits
SELECT pr,id,row_number() OVER (PARTITION BY pr ORDER BY depth DESC) FROM submitted;

CREATE TABLE catalog.commit_operations (
    commit_id uuid PRIMARY KEY REFERENCES catalog.commits(id),
    operation text NOT NULL CHECK (operation IN ('commit','fork','merge','cherry_pick','revert','restore','amend','squash','reset','rebase','baseline','integrate')),
    source_ids uuid[] NOT NULL DEFAULT '{}'
);
INSERT INTO catalog.commit_operations SELECT id,CASE WHEN kind='rebase' THEN 'rebase' ELSE 'commit' END,'{}' FROM catalog.commits;
CREATE TABLE catalog.commit_replacements (
    original_id uuid NOT NULL REFERENCES catalog.commits(id),
    replacement_id uuid NOT NULL REFERENCES catalog.commits(id),
    PRIMARY KEY (original_id,replacement_id),
    CHECK (original_id<>replacement_id)
);

CREATE TABLE catalog.workspace_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id),
    request_id uuid NOT NULL,
    request_hash text NOT NULL,
    actor_id text NOT NULL,
    action text NOT NULL,
    old_head uuid REFERENCES catalog.commits(id),
    new_head uuid REFERENCES catalog.commits(id),
    detail jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id,request_id)
);
CREATE INDEX workspace_events_history_idx ON catalog.workspace_events(workspace_id,id DESC);

CREATE TABLE catalog.workspace_tags (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id),
    name text NOT NULL CHECK (name<>'' AND length(name)<=200),
    commit_id uuid NOT NULL REFERENCES catalog.commits(id),
    message text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id,name)
);
CREATE TABLE catalog.workspace_stashes (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES catalog.workspaces(id),
    name text NOT NULL,
    objects jsonb NOT NULL CHECK (jsonb_typeof(objects)='array'),
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','dropped')),
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One explicit cutover snapshot, followed by sparse transactional deltas.
-- Main head is updated only at transaction finalization, after scoped fact work.
CREATE TABLE catalog.catalog_versions (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    commit_id uuid NOT NULL UNIQUE REFERENCES catalog.commits(id)
);
CREATE TABLE catalog.catalog_head (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    commit_id uuid NOT NULL REFERENCES catalog.commits(id)
);
CREATE TABLE catalog.catalog_record_versions (
    sequence bigint NOT NULL REFERENCES catalog.catalog_versions(sequence),
    target text NOT NULL CHECK(target IN ('entity','relation')),
    target_id uuid NOT NULL,
    document jsonb,
    PRIMARY KEY(sequence,target,target_id)
);
CREATE INDEX catalog_record_versions_lookup_idx ON catalog.catalog_record_versions(target,target_id,sequence DESC);
CREATE TABLE catalog.pending_record_versions (
    transaction_id bigint NOT NULL,
    target text NOT NULL,
    target_id uuid NOT NULL,
    PRIMARY KEY(transaction_id,target,target_id)
);

INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,message,kind,merged_at,merged_by,snapshot_scope)
VALUES('f3c10000-0037-7000-8000-000000000000','system',encode(sha256(convert_to('{"operation":"baseline","migration":37}','UTF8')),'hex'),'{"operation":"baseline","migration":37}','{}','{"id":"system","username":"system"}','Initial catalog snapshot','rebase',now(),'system','catalog');
INSERT INTO catalog.commit_operations VALUES('f3c10000-0037-7000-8000-000000000000','baseline','{}');
INSERT INTO catalog.catalog_versions(commit_id) VALUES('f3c10000-0037-7000-8000-000000000000');
INSERT INTO catalog.catalog_head(commit_id) VALUES('f3c10000-0037-7000-8000-000000000000');
INSERT INTO catalog.catalog_record_versions(sequence,target,target_id,document)
SELECT 1,'entity',e.id,
    (e.document-ARRAY['work_id','release_id','medium_id','parent_id','content_unit_id','contents','subjects']) ||
    jsonb_strip_nulls(jsonb_build_object('id',e.id,'kind',e.kind,'version',e.version,'title',e.title,
        'state',e.state,'created_by',e.created_by,
        'work_id',COALESCE(cu.work_id,ex.work_id), 'release_id',m.release_id,'medium_id',t.medium_id,
        'parent_id',COALESCE(cu.parent_id,m.parent_id,t.parent_id),'content_unit_id',ex.content_unit_id,
        'contents',CASE WHEN e.kind='track' THEN (SELECT NULLIF(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'expression_id',x.expression_id,'position',x.position,'locator',x.locator,
            'attributes',NULLIF(x.attributes,'{}'::jsonb),'sources',NULLIF(x.sources,'[]'::jsonb))) ORDER BY x.position),'[]'::jsonb)
            FROM catalog.track_contents x WHERE x.track_id=e.id) END,
        'subjects',CASE WHEN e.kind='release' THEN (SELECT NULLIF(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'work_id',x.work_id,'role',x.role,'position',x.position,'attributes',NULLIF(x.attributes,'{}'::jsonb))) ORDER BY x.position,x.work_id),'[]'::jsonb)
            FROM catalog.release_subjects x WHERE x.release_id=e.id) END))
FROM catalog.entities e LEFT JOIN catalog.content_units cu ON cu.id=e.id
LEFT JOIN catalog.expressions ex ON ex.id=e.id LEFT JOIN catalog.mediums m ON m.id=e.id
LEFT JOIN catalog.tracks t ON t.id=e.id;
INSERT INTO catalog.catalog_record_versions SELECT 1,'relation',id,document FROM catalog.relations;

CREATE FUNCTION catalog.queue_record_version() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE new_id uuid; old_id uuid;
BEGIN
    IF TG_OP<>'DELETE' THEN new_id:=(to_jsonb(NEW)->>TG_ARGV[1])::uuid; END IF;
    IF TG_OP<>'INSERT' THEN old_id:=(to_jsonb(OLD)->>TG_ARGV[1])::uuid; END IF;
    IF new_id IS NOT NULL THEN INSERT INTO catalog.pending_record_versions VALUES(txid_current(),TG_ARGV[0],new_id) ON CONFLICT DO NOTHING; END IF;
    IF old_id IS NOT NULL AND old_id IS DISTINCT FROM new_id THEN INSERT INTO catalog.pending_record_versions VALUES(txid_current(),TG_ARGV[0],old_id) ON CONFLICT DO NOTHING; END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER record_version_entities AFTER INSERT OR UPDATE OR DELETE ON catalog.entities FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','id');
CREATE TRIGGER record_version_content_units AFTER INSERT OR UPDATE OR DELETE ON catalog.content_units FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','id');
CREATE TRIGGER record_version_expressions AFTER INSERT OR UPDATE OR DELETE ON catalog.expressions FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','id');
CREATE TRIGGER record_version_mediums AFTER INSERT OR UPDATE OR DELETE ON catalog.mediums FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','id');
CREATE TRIGGER record_version_tracks AFTER INSERT OR UPDATE OR DELETE ON catalog.tracks FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','id');
CREATE TRIGGER record_version_subjects AFTER INSERT OR UPDATE OR DELETE ON catalog.release_subjects FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','release_id');
CREATE TRIGGER record_version_contents AFTER INSERT OR UPDATE OR DELETE ON catalog.track_contents FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('entity','track_id');
CREATE TRIGGER record_version_relations AFTER INSERT OR UPDATE OR DELETE ON catalog.relations FOR EACH ROW EXECUTE FUNCTION catalog.queue_record_version('relation','id');

CREATE FUNCTION catalog.reject_version_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'immutable version history' USING ERRCODE='23514'; END $$;
CREATE TRIGGER commit_objects_immutable BEFORE UPDATE OR DELETE ON catalog.commit_objects
FOR EACH ROW EXECUTE FUNCTION catalog.reject_version_history_mutation();
CREATE TRIGGER commit_snapshots_immutable BEFORE UPDATE OR DELETE ON catalog.commit_snapshots
FOR EACH ROW EXECUTE FUNCTION catalog.reject_version_history_mutation();
CREATE TRIGGER commit_operations_immutable BEFORE UPDATE OR DELETE ON catalog.commit_operations
FOR EACH ROW EXECUTE FUNCTION catalog.reject_version_history_mutation();
CREATE TRIGGER workspace_events_immutable BEFORE UPDATE OR DELETE ON catalog.workspace_events
FOR EACH ROW EXECUTE FUNCTION catalog.reject_version_history_mutation();
CREATE TRIGGER catalog_record_versions_immutable BEFORE UPDATE OR DELETE ON catalog.catalog_record_versions
FOR EACH ROW EXECUTE FUNCTION catalog.reject_version_history_mutation();

CREATE FUNCTION catalog.protect_commit_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN RAISE EXCEPTION 'immutable commit' USING ERRCODE='23514'; END IF;
    IF (to_jsonb(NEW)-ARRAY['merged_at','merged_by','pull_request_id']) IS DISTINCT FROM
       (to_jsonb(OLD)-ARRAY['merged_at','merged_by','pull_request_id']) THEN
        RAISE EXCEPTION 'immutable commit content' USING ERRCODE='23514';
    END IF;
    IF OLD.merged_at IS NOT NULL AND (NEW.merged_at,NEW.merged_by,NEW.pull_request_id)
        IS DISTINCT FROM (OLD.merged_at,OLD.merged_by,OLD.pull_request_id) THEN
        RAISE EXCEPTION 'immutable integration receipt' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER commit_content_immutable BEFORE UPDATE OR DELETE ON catalog.commits
FOR EACH ROW EXECUTE FUNCTION catalog.protect_commit_content();

UPDATE catalog.schema_contract SET version=37 WHERE singleton;
