import { useEffect, useRef } from 'react';
import type { useFavorites } from './useFavorites';

// One-time migration for the existing administrator browser. Remove this
// component after the user's 15 references are verified in Git and ConfigMap.
export default function LegacyFavoritesMigration({ subject, legacy, favorites, blocked, onImported }: {
 subject: string; legacy: string[]; favorites: ReturnType<typeof useFavorites>; blocked: boolean; onImported: (ids: string[]) => void;
}) {
 const attempted = useRef('');
 const callback = useRef(onImported); callback.current = onImported;
 const ids = [...new Set(legacy)];
 const key = subject + ':' + favorites.state?.version + ':' + JSON.stringify(ids);
 useEffect(() => {
  if (!subject || !favorites.state || !ids.length || blocked || favorites.dirty || attempted.current === key) return;
  attempted.current = key;
  const profile = structuredClone(favorites.state.profile);
  let group = profile.collections.find(c => c.id === 'daily') || profile.collections[0];
  if (!group) { group = { id: 'daily', name: '일상', cards: [] }; profile.collections.push(group); }
  group.cards = [...new Set([...group.cards, ...ids])];
  void favorites.persist({ profile, version: favorites.state.version }).then(async result => {
   if (result?.ok) callback.current(ids);
   else if (result?.error?.includes('다른 기기')) await favorites.reload();
  });
 }, [key, blocked, favorites.dirty]);
 return null;
}
