const test = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const vm = require('node:vm');
const ts = require('typescript');

// Execute the production hook with render and passive-effect phases kept separate.
// This makes the interval before a new identity's effect runs observable without a DOM.
const text = readFileSync(require.resolve('../src/components/work/WorkContentDirectory.tsx'), 'utf8');
const source = ts.createSourceFile('WorkContentDirectory.tsx', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const hook = source.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === 'useWorkDirectoryData');
assert.ok(hook);
const code = ts.transpileModule(hook.getText(source), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function harness(initialViewer, response) {
  let viewer = initialViewer;
  let cursor = 0;
  const slots = [];
  const effects = new Map();
  const pending = new Map();
  const calls = [];
  const request = (url) => {
    calls.push({ url, viewer });
    return response(url, viewer);
  };
  const context = {
    exports: {},
    useAuth: () => ({ user: viewer ? { id: viewer } : null }),
    useState(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = initial;
      return [slots[index], (value) => {
        slots[index] = typeof value === 'function' ? value(slots[index]) : value;
      }];
    },
    useRef(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = { current: initial };
      return slots[index];
    },
    useEffect(callback, dependencies) {
      const index = cursor++;
      const previous = effects.get(index);
      if (!previous || dependencies.some((value, i) => value !== previous.dependencies[i])) {
        effects.set(index, { dependencies: [...dependencies], cleanup: previous?.cleanup });
        pending.set(index, callback);
      }
    },
    fetchAllPages: request,
    fetchEntityRelationships: (id) => request(`/catalog/entities/${id}/links`),
  };
  vm.runInNewContext(code, context);
  return {
    calls,
    setViewer(value) { viewer = value; },
    render(workId) { cursor = 0; return context.exports.useWorkDirectoryData(workId); },
    flushEffects() {
      for (const [index, callback] of pending) {
        const effect = effects.get(index);
        effect.cleanup?.();
        effect.cleanup = callback();
      }
      pending.clear();
    },
  };
}

const settle = () => new Promise((resolve) => setImmediate(resolve));
const items = (data) => Array.from(data.items, (item) => item.id);
function visibleData(url, viewer) {
  if (url.includes('/links')) return Promise.resolve({ items: [], entities: {} });
  return Promise.resolve(url.includes('kind=content_unit')
    ? [{ id: viewer === 'admin' ? 'private-chapter' : 'public-chapter' }]
    : []);
}

test('logout masks cached private directory before the public work effect runs', async () => {
  const h = harness('admin', visibleData);
  h.render('public-work'); h.flushEffects(); await settle();
  assert.deepEqual(items(h.render('public-work')), ['private-chapter']);

  h.setViewer('');
  h.render(''); h.flushEffects();
  const beforeEffect = h.render('public-work');
  assert.equal(beforeEffect.status, 'loading');
  assert.deepEqual(items(beforeEffect), []);
  h.flushEffects(); await settle();
  assert.deepEqual(items(h.render('public-work')), ['public-chapter']);
});

test('switching viewers on the same work immediately masks data and reloads it', async () => {
  const h = harness('admin', visibleData);
  h.render('public-work'); h.flushEffects(); await settle();
  assert.deepEqual(items(h.render('public-work')), ['private-chapter']);

  h.setViewer('reader');
  assert.deepEqual(items(h.render('public-work')), []);
  h.flushEffects(); await settle();
  assert.deepEqual(items(h.render('public-work')), ['public-chapter']);
  assert.equal(h.calls.filter((call) => call.viewer === 'reader').length, 3);
});

test('late responses from an old viewer cannot replace the current directory', async () => {
  let finishOld;
  const oldUnits = new Promise((resolve) => { finishOld = resolve; });
  const h = harness('admin', (url, viewer) => viewer === 'admin' && url.includes('kind=content_unit')
    ? oldUnits : visibleData(url, viewer));
  h.render('public-work'); h.flushEffects();
  h.setViewer('reader');
  h.render('public-work'); h.flushEffects(); await settle();
  assert.deepEqual(items(h.render('public-work')), ['public-chapter']);
  finishOld([{ id: 'private-chapter' }]); await settle();
  assert.deepEqual(items(h.render('public-work')), ['public-chapter']);
});

test('clearing the work clears cached data and invalidates pending responses', async () => {
  let finishOld;
  const oldUnits = new Promise((resolve) => { finishOld = resolve; });
  const h = harness('admin', (url, viewer) => url.includes('kind=content_unit')
    ? oldUnits : visibleData(url, viewer));
  h.render('public-work'); h.flushEffects();
  h.render(''); h.flushEffects();
  finishOld([{ id: 'private-chapter' }]); await settle();
  const empty = h.render('');
  assert.equal(empty.status, 'ready');
  assert.deepEqual(items(empty), []);
});
