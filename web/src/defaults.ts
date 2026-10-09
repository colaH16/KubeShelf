import type { Settings } from './types';
export type DefaultChanges = { target?: string; addresses: Record<string, string> };
export const emptyDefaults = (): DefaultChanges => ({ addresses: {} });
export const defaultChangeCount = (changes: DefaultChanges) => Number(changes.target !== undefined) + Object.keys(changes.addresses).length;
export function applyDefaults(settings: Settings, changes: DefaultChanges): Settings {
  const next = structuredClone(settings);
  if (changes.target !== undefined) next.defaultTarget = changes.target;
  for (const [id, endpointID] of Object.entries(changes.addresses)) {
    (next.apps[id] ||= { hidden: false }).defaultEndpoint = endpointID;
  }
  return next;
}
