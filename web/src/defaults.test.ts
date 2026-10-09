import { expect, it } from 'vitest';
import { applyDefaults, defaultChangeCount } from './defaults';
import type { Settings } from './types';

it('merges a batch into the latest settings without changing unrelated permissions or review state', () => {
 const latest: Settings = { schemaVersion:1, revision:'latest', namespaces:{ public:{ mode:'public', hidden:true } }, nodePortNamespaces:{ public:{ mode:'admin' } }, apps:{ cloud:{ hidden:false, name:'Cloud', reviewed:{ a:'fingerprint' }, visibility:{ mode:'admin' } } }, targets:[], manual:[], assignments:{ a:'cloud' } };
 const before = structuredClone(latest);
 const changes = { target:'node-node3', addresses:{ cloud:'b', second:'c' } };
 const result = applyDefaults(latest, changes);
 expect(defaultChangeCount(changes)).toBe(3);
 expect(result.defaultTarget).toBe('node-node3');
 expect(result.apps.cloud).toEqual({ ...latest.apps.cloud, defaultEndpoint:'b' });
 expect(result.apps.second).toEqual({ hidden:false, defaultEndpoint:'c' });
 expect(result.namespaces).toEqual(latest.namespaces);
 expect(result.nodePortNamespaces).toEqual(latest.nodePortNamespaces);
 expect(result.assignments).toEqual(latest.assignments);
 expect(latest).toEqual(before);
});
