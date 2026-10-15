const test = require('node:test');
const assert = require('node:assert/strict');
const { revisionChanges, prepareRevisionRestore, canEditRevision } = require('../src/components/catalog/revisionData');

test('history includes structural content, authority IDs and complete translated text', () => {
  const before = { subjects: [{ work_id: 'song' }], contents: [], translations: { ja: { title: 'Song', summary: 'old' } }, pictures: [{ url: 'cover', caption: { en: 'old' } }] };
  const after = { subjects: [{ work_id: 'song', attributes: { role: 'bonus' } }], contents: [{ expression_id: 'recording' }], translations: { ja: { title: 'Song', summary: 'new' } }, pictures: [{ url: 'cover', caption: { en: 'new' } }], external_links: [{ kind: 'isrc', url: 'mf://external/isrc/code', external_id: 'code' }] };
  const diff = revisionChanges(before, after);
  assert.deepEqual(Object.keys(diff).sort(), ['contents', 'external_links', 'pictures', 'subjects', 'translations.ja.summary']);
  assert.equal(diff['translations.ja.summary'].new, 'new');
});

test('object key order is irrelevant but list order is meaningful', () => {
  assert.deepEqual(revisionChanges({ attributes: { data: { a: 1, b: 2 } } }, { attributes: { data: { b: 2, a: 1 } } }), {});
  assert.ok(revisionChanges({ contents: ['a', 'b'] }, { contents: ['b', 'a'] }).contents);
  assert.deepEqual(revisionChanges({ version: 1, updated_at: 'old' }, { version: 2, updated_at: 'new' }), {});
});

const current = { id: 'entity', kind: 'track', version: 6, state: 'active', created_by: 'owner', medium_id: 'disc', parent_id: 'side', title: 'Current', attributes: { number: 2 }, contents: [] };
const snapshot = { id: 'entity', kind: 'track', version: 3, state: 'active', created_by: 'other', medium_id: 'disc', title: 'Historical', attributes: { number: 1 }, contents: [{ expression_id: 'recording', locator: { relative_to: 'track' } }] };

test('restore retains latest lock, lifecycle and owner, restores complete content', () => {
  const restored = prepareRevisionRestore(current, snapshot);
  assert.equal(restored.version, 6);
  assert.equal(restored.state, 'active');
  assert.equal(restored.created_by, 'owner');
  assert.equal(restored.title, 'Historical');
  assert.equal(restored.parent_id, undefined);
  assert.deepEqual(restored.contents, snapshot.contents);
  restored.attributes.number = 9;
  assert.equal(snapshot.attributes.number, 1);
  assert.equal(current.attributes.number, 2);
});

test('restore rejects other identities, incompatible scopes and retired entities', () => {
  for (const patch of [{ id: 'other' }, { kind: 'work' }, { medium_id: 'other' }, { state: 'merged' }]) {
    assert.throws(() => prepareRevisionRestore(current, { ...snapshot, ...patch }), /revision_incompatible/);
  }
  assert.throws(() => prepareRevisionRestore({ ...current, state: 'deleted' }, snapshot));
});

test('restore eligibility respects the current permission codes', () => {
  assert.equal(canEditRevision(current, undefined), false);
  assert.equal(canEditRevision(current, { id: 'other', permissions: ['catalog.entity.edit'] }), true);
  assert.equal(canEditRevision({ ...current, state: 'active' }, { id: 'other', permissions: ['catalog.entity.edit'] }), true);
  assert.equal(canEditRevision(current, { id: 'owner', permissions: [] }), true);
  assert.equal(canEditRevision(current, { id: 'owner', permissions: ['catalog.entity.edit'] }), true);
  assert.equal(canEditRevision(current, { id: 'admin', permissions: ['catalog.lifecycle.manage'] }), true);
  assert.equal(canEditRevision({ ...current, state: 'active' }, { id: 'owner', permissions: [] }), true);
});
