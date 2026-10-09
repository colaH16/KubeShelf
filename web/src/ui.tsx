import { useEffect, useRef, type ReactNode } from 'react';
import { Activity, BarChart3, Box, Boxes, Cloud, Database, Folder, Globe, Image, Layers3, NotebookPen, Server, Shield, Terminal, Users, X } from 'lucide-react';
import type { Directory, Policy } from './types';
const icons: Record<string, typeof Cloud> = { cloud: Cloud, photos: Image, chart: BarChart3, box: Box, boxes: Boxes, notebook: NotebookPen, server: Server, database: Database, shield: Shield, activity: Activity, terminal: Terminal, globe: Globe, folder: Folder, users: Users, layers: Layers3 };
export const iconNames = Object.keys(icons);
export function AppIcon({ icon, name }: { icon: string; name: string }) {
  const lower = name.toLowerCase();
  const automatic = lower.includes('cloud') ? 'cloud' : lower.includes('immich') || lower.includes('photo') ? 'photos' : lower.includes('grafana') ? 'chart' : lower.includes('rancher') ? 'boxes' : lower.includes('redis') || lower.includes('sql') ? 'database' : lower.includes('kuma') ? 'activity' : lower.includes('note') || lower.includes('bullet') ? 'notebook' : lower.includes('proxmox') ? 'server' : 'box';
  const C = icons[icon] || icons[automatic];
  if (/^https?:\/\//.test(icon)) return <img className="custom-icon" src={icon} alt="" referrerPolicy="no-referrer" />;
  return <C size={25} strokeWidth={1.65} />;
}
export function Modal({ title, subtitle, children, onClose, footer, className = '' }: { title: string; subtitle?: string; children: ReactNode; onClose: () => void; footer?: ReactNode; className?: string }) {
  const dialog = useRef<HTMLElement>(null);
  const close = useRef(onClose); close.current = onClose;
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden'; dialog.current?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); close.current(); }
      if (e.key !== 'Tab') return;
      const elements = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]') || []).filter(el => el.getClientRects().length);
      const first = elements[0], last = elements[elements.length - 1];
      if (!first) { e.preventDefault(); return; }
      if (e.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && (document.activeElement === last || document.activeElement === dialog.current)) { e.preventDefault(); first.focus(); }
    };
    document.addEventListener('keydown', key);
    return () => { document.body.style.overflow = overflow; document.removeEventListener('keydown', key); previous?.focus(); };
  }, []);
  return <div className="modal-backdrop" onClick={e => { if (e.target === e.currentTarget) onClose(); }}><section ref={dialog} tabIndex={-1} role="dialog" aria-modal="true" aria-label={title} className={'modal ' + className}><header><div><h2>{title}</h2>{subtitle && <p>{subtitle}</p>}</div><button className="icon-button" aria-label="닫기" onClick={onClose}><X size={20}/></button></header><div className="modal-body">{children}</div>{footer && <footer>{footer}</footer>}</section></div>;
}
export function PolicyEditor({ value, onChange, directory, inherit = true }: { value?: Policy; onChange: (p: Policy | undefined) => void; directory: Directory; inherit?: boolean }) {
  const toggle = (key: 'groups' | 'users', id: string) => { const list = value?.[key] || []; onChange({ ...value!, [key]: list.includes(id) ? list.filter(x => x !== id) : [...list, id] }); };
  return <div className="policy-editor"><select aria-label="표시 범위" value={value?.mode || 'inherit'} onChange={e => onChange(e.target.value === 'inherit' ? undefined : { mode: e.target.value as Policy['mode'], groups: value?.groups || [], users: value?.users || [] })}>{inherit && <option value="inherit">상위 설정 따름</option>}<option value="public">누구나 · 로그인 없이</option><option value="restricted">선택한 그룹 또는 사용자</option><option value="admin">관리자만</option></select>{value?.mode === 'restricted' && <div className="permission-options"><div><span className="field-caption">그룹</span>{directory.groups.map(g => <label className="check-row" key={g}><input type="checkbox" checked={value.groups?.includes(g) || false} onChange={() => toggle('groups', g)}/>{g}</label>)}</div><div><span className="field-caption">사용자</span>{directory.users.map(u => <label className="check-row" key={u.subject}><input type="checkbox" checked={value.users?.includes(u.subject) || false} onChange={() => toggle('users', u.subject)}/><span>{u.name || u.username}<small>{u.username}</small></span></label>)}</div><p className="muted">선택한 그룹 또는 사용자 중 하나에 해당하면 볼 수 있습니다.</p></div>}</div>;
}
