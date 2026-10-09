import { useEffect, useRef, useState } from 'react';
import { applyDefaults, defaultChangeCount, emptyDefaults, type DefaultChanges } from './defaults';
import type { AdminState, ApplyStatus, Card, Catalog, Settings } from './types';

export function firstTarget(catalog?: Catalog) {
  return catalog?.cards.flatMap(c => c.endpoints).find(e => e.kind === 'nodeport' && e.local && e.targets.length)?.targets[0]?.targetId || catalog?.targets[0]?.id || '';
}

export function useDefaults(catalog: Catalog | undefined, status: ApplyStatus | undefined, busy: boolean,
  fetchState: () => Promise<AdminState>,
  save: (settings: Settings, base: string, review: string[]) => Promise<{ revision: string }>) {
  const [draft, setDraft] = useState<DefaultChanges>(emptyDefaults);
  const [committed, setCommitted] = useState<{ revision: string; changes: DefaultChanges }>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const lock = useRef(false);
  const identity = catalog?.identity.subject || '';
  useEffect(() => { setDraft(emptyDefaults()); setCommitted(undefined); setError(''); }, [identity]);
  useEffect(() => {
    if (committed && (catalog?.appliedRevision === committed.revision || (status && !status.pending && status.appliedRevision === catalog?.appliedRevision))) setCommitted(undefined);
  }, [catalog?.appliedRevision, status, committed]);
  const count = defaultChangeCount(draft);
  useEffect(() => {
    if (!count) return;
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ''; };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [count]);
  const savedTarget = committed?.changes.target ?? catalog?.defaultTarget;
  const savedAddress = (card: Card) => committed?.changes.addresses[card.id] ?? card.defaultEndpoint;
  const target = draft.target ?? savedTarget;
  const address = (card: Card) => draft.addresses[card.id] ?? savedAddress(card);
  const selectTarget = (id: string) => {
    if (lock.current || busy) return;
    setError('');
    setDraft(old => ({ ...old, target: id === (savedTarget || firstTarget(catalog)) ? undefined : id }));
  };
  const selectAddress = (card: Card, id: string) => {
    if (lock.current || busy) return;
    setError('');
    setDraft(old => {
      const addresses = { ...old.addresses };
      if (id === (savedAddress(card) || card.endpoints[0]?.id)) delete addresses[card.id];
      else addresses[card.id] = id;
      return { ...old, addresses };
    });
  };
  const persist = async () => {
    if (lock.current || busy || !count || !catalog?.identity.admin) return;
    lock.current = true; setSaving(true); setError('');
    const changes = structuredClone(draft);
    try {
      // Fetch immediately before merging so unrelated edits from another window
      // are preserved. The existing baseCommit check also guards a later race.
      const latest = await fetchState();
      const review = latest.catalog.cards.filter(c => c.source !== 'custom' && changes.addresses[c.id] !== undefined).flatMap(c => c.endpoints.map(e => e.id));
      const result = await save(applyDefaults(latest.settings, changes), latest.baseCommit, review);
      setCommitted(old => ({ revision: result.revision, changes: { ...old?.changes, ...changes, target: changes.target ?? old?.changes.target, addresses: { ...old?.changes.addresses, ...changes.addresses } } }));
      setDraft(emptyDefaults());
    } catch (e) { setError((e as Error).message); }
    finally { lock.current = false; setSaving(false); }
  };
  return { count, saving, error, target, address, savedTarget, savedAddress, selectTarget, selectAddress, persist,
    reset: () => { if (!lock.current && !busy) { setDraft(emptyDefaults()); setError(''); } } };
}
