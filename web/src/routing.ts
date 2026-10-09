import { useCallback, useEffect, useState, type MouseEvent } from 'react';

export const viewPaths = { all: '/', favorites: '/favorites', discovery: '/discovery', namespaces: '/namespaces', hidden: '/hidden' } as const;
export type View = keyof typeof viewPaths;
export function viewForPath(path: string): View {
  return (Object.keys(viewPaths) as View[]).find(view => viewPaths[view] === path) || 'all';
}
export function adminView(view: View) { return view === 'discovery' || view === 'namespaces' || view === 'hidden'; }
export function normalNavigation(e: MouseEvent) { return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey; }
export function useViewRoute() {
  const [view, setView] = useState<View>(() => viewForPath(window.location.pathname));
  useEffect(() => {
    const update = () => setView(viewForPath(window.location.pathname));
    window.addEventListener('popstate', update);
    return () => window.removeEventListener('popstate', update);
  }, []);
  const navigate = useCallback((next: View) => {
    if (window.location.pathname !== viewPaths[next]) window.history.pushState(null, '', viewPaths[next]);
    setView(next);
  }, []);
  return [view, navigate] as const;
}
