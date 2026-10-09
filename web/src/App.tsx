import { useCallback, useEffect, useRef, useState } from 'react';
import { Activity, ArrowDownToLine, ArrowUpRight, Boxes, Check, ChevronDown, Copy, EyeOff, FolderTree, Globe, Inbox, Layers3, LoaderCircle, LockKeyhole, LogIn, LogOut, Menu, Network, Pencil, Plus, Search, Server, Settings2, ShieldCheck, Star, X } from 'lucide-react';
import Editor, { type EditRequest } from './Editor';
import { AppIcon } from './ui';
import { chooseTarget, policyLabel } from './selection';
import type { AdminState, ApplyStatus, Card, Catalog, Endpoint, Settings } from './types';
type View = 'all' | 'favorites' | 'discovery' | 'namespaces' | 'hidden';
type Preferences = { favorites: string[]; selected: Record<string, string>; target: string; previous: Record<string, string> };
const emptyPrefs = (): Preferences => ({ favorites: [], selected: {}, target: '', previous: {} });
async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, { ...options, credentials: 'same-origin', cache: 'no-store', headers: { 'custom-cf': 'bypass-cache', ...(options?.body ? { 'Content-Type': 'application/json' } : {}), ...options?.headers } });
  if (!response.ok) { let message = `요청에 실패했습니다 (${response.status})`; try { message = (await response.json()).error || message; } catch { /* generic message */ } throw new Error(message); }
  if (response.status === 204) return undefined as T;
  return response.json();
}
function safePrefs(key: string): Preferences { try { const v = JSON.parse(localStorage.getItem(key) || '{}'); return { favorites: Array.isArray(v.favorites) ? v.favorites.filter((x: unknown) => typeof x === 'string') : [], selected: v.selected && typeof v.selected === 'object' ? v.selected : {}, target: typeof v.target === 'string' ? v.target : '', previous: v.previous && typeof v.previous === 'object' ? v.previous : {} }; } catch { return emptyPrefs(); } }
export default function App() {
  const [catalog, setCatalog] = useState<Catalog>();
  const [error, setError] = useState('');
  const [view, setView] = useState<View>('all');
  const [query, setQuery] = useState('');
  const [prefs, setPrefs] = useState<Preferences>(emptyPrefs);
  const [admin, setAdmin] = useState<AdminState>();
  const [editor, setEditor] = useState<EditRequest>();
  const [status, setStatus] = useState<ApplyStatus>();
  const [saving, setSaving] = useState(false);
  const [opening, setOpening] = useState(false);
  const [toast, setToast] = useState('');
  const [mobile, setMobile] = useState(false);
  const who = useRef<string | null>(null);
  const saveLock = useRef(false);
  const mounted = useRef(true);
  const requestNumber = useRef(0);
  const loggingOut = useRef(false);
  const refresh = useCallback(async () => {
    if (loggingOut.current) return;
    const request = ++requestNumber.current;
    try {
      const cat = await api<Catalog>('/api/catalog');
      if (!mounted.current || request !== requestNumber.current || loggingOut.current) return;
      const identity = cat.identity.subject || 'anonymous';
      if (who.current !== identity) { who.current = identity; setPrefs(safePrefs('kubeshelf:' + identity)); setAdmin(undefined); setEditor(undefined); setStatus(undefined); setView('all'); setQuery(''); }
      setCatalog(cat); setError('');
      if (cat.identity.admin && !saveLock.current) { const next = await api<ApplyStatus>('/api/admin/status'); if (mounted.current && request === requestNumber.current && who.current === identity) setStatus(next); }
    } catch (e) { if (mounted.current && request === requestNumber.current && !loggingOut.current) setError((e as Error).message); }
  }, []);
  useEffect(() => { mounted.current = true; void refresh(); const timer = setInterval(() => void refresh(), 5000); return () => { mounted.current = false; clearInterval(timer); }; }, [refresh]);
  useEffect(() => { if (!toast) return; const timer = setTimeout(() => setToast(''), 4000); return () => clearTimeout(timer); }, [toast]);
  useEffect(() => { if (who.current !== null) { try { localStorage.setItem('kubeshelf:' + who.current, JSON.stringify(prefs)); } catch { /* private browser storage can be unavailable */ } } }, [prefs]);
  useEffect(() => {
    if (!catalog) return;
    setPrefs(old => {
      const next = structuredClone(old);
      if (!catalog.targets.some(t => t.id === next.target)) { const firstLocal = catalog.cards.flatMap(c => c.endpoints).find(e => e.kind === 'nodeport' && e.local && e.targets.length); next.target = firstLocal?.targets[0]?.targetId || catalog.targets[0]?.id || ''; }
      for (const e of catalog.cards.flatMap(c => c.endpoints)) { if (e.kind !== 'nodeport') continue; const target = chooseTarget(next.target, next.previous[e.id], e.targets); if (target) next.previous[e.id] = target.targetId; }
      return JSON.stringify(next) === JSON.stringify(old) ? old : next;
    });
  }, [catalog, prefs.target]);
  const notice = (message: string) => setToast(message);
  const openEditor = async (request: EditRequest) => { const identity = who.current; setOpening(true); try { const data = await api<AdminState>('/api/admin/state'); if (loggingOut.current || who.current !== identity) return; setAdmin(data); setEditor(request); } catch (e) { notice((e as Error).message); } finally { setOpening(false); } };
  const save = async (settings: Settings, baseCommit: string, reviewEndpoints: string[] = []) => {
    if (saveLock.current) throw new Error('다른 저장이 끝날 때까지 기다려 주세요');
    saveLock.current = true; ++requestNumber.current; setSaving(true);
    try {
      const result = await api<{ status: ApplyStatus; baseCommit: string; settings: Settings }>('/api/admin/state', { method: 'PUT', headers: { 'X-CSRF-Token': catalog?.csrf || '' }, body: JSON.stringify({ settings, baseCommit, reviewEndpoints }) });
      setStatus(result.status); setAdmin(old => old ? { ...old, settings: result.settings, baseCommit: result.baseCommit, status: result.status } : old); notice('Git에 저장했어요. 설정 적용을 기다리고 있습니다.');
      return { baseCommit: result.baseCommit, revision: result.status.desiredRevision, settings: result.settings };
    } finally { saveLock.current = false; setSaving(false); }
  };
  const restore = async (card: Card) => { try { const data = await api<AdminState>('/api/admin/state'); const next = structuredClone(data.settings); for (const id of new Set([card.id, ...card.endpoints.map(e => e.appId)])) if (next.apps[id]) next.apps[id].hidden = false; await save(next, data.baseCommit); } catch (e) { notice((e as Error).message); } };
  const logout = async () => {
    if (loggingOut.current) return;
    loggingOut.current = true; ++requestNumber.current;
    setCatalog(undefined); setAdmin(undefined); setEditor(undefined); setStatus(undefined); setView('all'); setQuery(''); setPrefs(emptyPrefs()); who.current = null;
    try { await api('/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': catalog?.csrf || '' } }); }
    catch (e) { notice((e as Error).message); }
    finally { loggingOut.current = false; await refresh(); }
  };
  const copy = async (text: string) => { try { await navigator.clipboard.writeText(text); notice('주소를 복사했어요'); } catch { notice('주소를 복사하지 못했어요'); } };
  const favorite = (id: string) => setPrefs(p => ({ ...p, favorites: p.favorites.includes(id) ? p.favorites.filter(v => v !== id) : [...p.favorites, id] }));
  const counts = { total: catalog?.cards.filter(c => !c.hidden).length || 0, favorites: catalog?.cards.filter(c => !c.hidden && prefs.favorites.includes(c.id)).length || 0, discovery: catalog?.cards.filter(c => c.new || c.changed).length || 0, namespaces: catalog?.namespaces?.filter(n => !n.configured).length || 0, hidden: catalog?.cards.filter(c => c.hidden).length || 0 };
  const move = (v: View) => { setView(v); setMobile(false); };
  const titles: Record<View, [string, string]> = { all: ['내 서비스', '흩어져 있던 앱을, 한 곳에.'], favorites: ['즐겨찾기', '자주 찾는 서비스에 더 빠르게.'], discovery: ['새로 발견했어요', '클러스터에서 찾은 서비스와 바뀐 주소를 확인하세요.'], namespaces: ['네임스페이스', '새로 발견되는 서비스의 기본 공개 범위를 관리하세요.'], hidden: ['숨긴 서비스', '필요할 때 다시 대시보드에 꺼내 놓으세요.'] };
  const cards = (catalog?.cards || []).filter(c => {
    if (view === 'hidden' ? !c.hidden : c.hidden) return false;
    if (view === 'favorites' && !prefs.favorites.includes(c.id)) return false;
    if (view === 'discovery' && !c.new && !c.changed) return false;
    const text = [c.name, c.description, c.namespace, ...c.endpoints.flatMap(e => [e.label, e.url, e.service])].join(' ').toLowerCase();
    return text.includes(query.toLowerCase());
  });
  const nodeSelect = <div className="node-control"><Server size={15}/><select aria-label="NodePort 접속 노드" value={prefs.target} onChange={e => setPrefs(p => ({ ...p, target: e.target.value }))}>{catalog?.targets.map(t => <option key={t.id} value={t.id}>{t.name}{t.manual ? ' · 도메인' : ''}</option>)}</select><ChevronDown size={13}/>{catalog?.identity.admin && <button className="icon-button" aria-label="NodePort 도메인 관리" disabled={opening} onClick={() => void openEditor({ kind: 'targets' })}><Settings2 size={16}/></button>}</div>;
  const cardView = (card: Card) => {
    const endpoint = card.endpoints.find(e => e.id === prefs.selected[card.id]) || card.endpoints[0];
    if (!endpoint) return null;
    const target = endpoint.kind === 'nodeport' ? chooseTarget(prefs.target, prefs.previous[endpoint.id], endpoint.targets) : undefined;
    const href = endpoint.kind === 'nodeport' ? target?.url : endpoint.url;
    const address = href || target?.address || '';
    const color = ['amber', 'blue', 'green', 'purple', 'pink'][Array.from(card.name).reduce((n, c) => n + c.charCodeAt(0), 0) % 5];
    return <article className={'service-card' + (card.hidden ? ' is-hidden' : '')} key={card.id}>
      <div className="card-controls"><span className={'health-dot ' + endpoint.health.state} title={`${endpoint.health.reason}${endpoint.health.total ? ` · ${endpoint.health.ready}/${endpoint.health.total}` : ''}`} aria-label={endpoint.health.reason}/><div className="card-badges">{(card.new || card.changed) && catalog?.identity.admin && <span className="new-badge">{card.changed ? 'CHANGED' : 'NEW'}</span>}{endpoint.local && <span className="local-badge" title="externalTrafficPolicy: Local">Local</span>}</div><button className={'icon-button favorite' + (prefs.favorites.includes(card.id) ? ' active' : '')} onClick={() => favorite(card.id)} aria-label={prefs.favorites.includes(card.id) ? '즐겨찾기 해제' : '즐겨찾기 추가'}><Star size={16}/></button>{catalog?.identity.admin && <button className="icon-button card-edit" aria-label={`${card.name} 설정`} disabled={opening} onClick={() => void openEditor({ kind: 'card', card })}><Pencil size={15}/></button>}</div>
      <a className="card-main" href={href || undefined} target={href ? '_blank' : undefined} rel="noopener noreferrer" aria-disabled={!href} onClick={e => { if (!href) { e.preventDefault(); if (address) void copy(address); } }}><span className={'app-icon tone-' + color}><AppIcon icon={card.icon} name={card.name}/></span><div><h3>{card.name}</h3><p title={card.description || card.namespace}>{card.description || card.namespace || '직접 추가한 서비스'}</p></div></a>
      <div className="card-address">{card.endpoints.length > 1 ? <select aria-label={`${card.name} 접속 주소`} value={endpoint.id} onChange={e => setPrefs(p => ({ ...p, selected: { ...p.selected, [card.id]: e.target.value } }))}>{card.endpoints.map(e => <option key={e.id} value={e.id}>{e.label || e.url || e.service}</option>)}</select> : <span title={address}>{endpoint.kind === 'nodeport' ? target ? `${target.name} : ${endpoint.nodePort}` : '사용 가능한 노드 없음' : endpoint.needsURL ? '접속 주소 설정 필요' : endpoint.label || endpoint.url}</span>}{address && <button className="icon-button copy" aria-label={`${card.name} 링크 복사`} onClick={() => void copy(address)}><Copy size={14}/></button>}{href ? <a href={href} target="_blank" rel="noopener noreferrer" className="open-link" aria-label={`${card.name} 열기`}><ArrowUpRight size={17}/></a> : <span className="muted">{endpoint.scheme === 'tcp' && address ? 'TCP' : '—'}</span>}</div>
      {(endpoint.local || endpoint.health.state === 'down' || card.hidden) && <div className="card-note">{endpoint.local && <span>{target ? `${target.name} 사용 중` : 'Ready Pod 없음'}{target && target.targetId !== prefs.target ? ' · 마지막 사용 가능 노드' : ''}</span>}{!endpoint.local && endpoint.health.state === 'down' && <span className="down-text">준비된 Pod 없음</span>}{card.hidden && <button disabled={saving} className="text-button" onClick={() => void restore(card)}>다시 표시</button>}</div>}
      {catalog?.identity.admin && target && catalog.targets.some(t => t.id === target.targetId && t.tcpEnabled) && <button className="tcp-button" onClick={async () => { try { const v = await api<{ message: string }>('/api/admin/tcp', { method: 'POST', headers: { 'X-CSRF-Token': catalog.csrf || '' }, body: JSON.stringify({ endpointId: endpoint.id, targetId: target.targetId }) }); notice(v.message + ' · 브라우저 접속 여부와는 별도입니다'); } catch (e) { notice((e as Error).message); } }}>TCP 연결 확인</button>}
    </article>;
  };
  return <div className="app-shell">
    {mobile && <div className="sidebar-shade" onClick={() => setMobile(false)}/>}
    <aside className={'sidebar' + (mobile ? ' expanded' : '')}><a className="brand" href="/" onClick={e => { e.preventDefault(); move('all'); }}><span className="brand-mark"><Layers3 size={23}/></span><span>KubeShelf<small>YOUR CLUSTER, ORGANIZED</small></span></a><div className="workspace"><span className="workspace-icon"><Boxes size={18}/></span><div><strong>My workspace</strong><small><span className={'tiny-dot ' + (catalog?.connected ? 'healthy' : 'unknown')}/>{catalog?.connected ? 'Kubernetes 연결됨' : '연결 확인 중'}</small></div><ChevronDown size={14}/></div><span className="nav-heading">WORKSPACE</span><nav><button className={view === 'all' ? 'active' : ''} onClick={() => move('all')}><Layers3 size={18}/>모든 서비스<span>{counts.total}</span></button><button className={view === 'favorites' ? 'active' : ''} onClick={() => move('favorites')}><Star size={18}/>즐겨찾기<span>{counts.favorites}</span></button>{catalog?.identity.admin && <><span className="nav-heading">MANAGE</span><button className={view === 'discovery' ? 'active' : ''} onClick={() => move('discovery')}><Inbox size={18}/>발견함{counts.discovery > 0 && <span className="nav-badge">{counts.discovery}</span>}</button><button className={view === 'namespaces' ? 'active' : ''} onClick={() => move('namespaces')}><FolderTree size={18}/>네임스페이스{counts.namespaces > 0 && <span className="nav-badge">{counts.namespaces}</span>}</button><button className={view === 'hidden' ? 'active' : ''} onClick={() => move('hidden')}><EyeOff size={18}/>숨긴 서비스<span>{counts.hidden}</span></button></>}</nav><div className="sidebar-bottom"><div className="quiet-note"><span className="tiny-dot healthy"/>A little order for your cluster.</div><div className="profile"><span className="avatar">{catalog?.identity.subject ? (catalog.identity.name || catalog.identity.username).slice(0, 1).toUpperCase() : <Globe size={18}/>}</span><div><strong>{catalog?.identity.subject ? catalog.identity.name || catalog.identity.username : '둘러보는 중'}</strong><small>{catalog?.identity.admin ? '관리자' : catalog?.identity.subject ? '로그인됨' : '공개 서비스'}</small></div>{catalog?.identity.subject ? <button className="icon-button" aria-label="로그아웃" onClick={() => void logout()}><LogOut size={17}/></button> : <a className="icon-button" aria-label="SSO 로그인" href="/auth/login"><LogIn size={18}/></a>}</div></div></aside>
    <div className="content-shell"><header className="topbar"><button className="icon-button mobile-menu" aria-label="메뉴" onClick={() => setMobile(true)}><Menu size={21}/></button><div className="breadcrumb">Workspace <span>/</span> <strong>{titles[view][0]}</strong></div><div className="top-actions">{catalog?.demo && <span className="demo-badge">DEMO</span>}{catalog?.identity.admin ? <span className="admin-badge"><ShieldCheck size={14}/>관리자</span> : !catalog?.identity.subject && <a className="button secondary small" href="/auth/login"><LogIn size={15}/>로그인</a>}<span className="top-avatar">KS</span></div></header>
    <main><div className="page-heading"><div><div className="eyebrow">YOUR PERSONAL APP SHELF</div><h1>{titles[view][0]}<span className="heading-dot">.</span></h1><p>{titles[view][1]}</p></div>{catalog?.identity.admin && <button className="button primary" disabled={opening} onClick={() => void openEditor({ kind: 'manual' })}>{opening ? <LoaderCircle size={17} className="spin"/> : <Plus size={17}/>}서비스 추가</button>}</div>
      {error && <div className="error-box" role="alert">{error}<button className="text-button" onClick={() => void refresh()}>다시 확인</button></div>}
      {catalog?.problem && <div className="notice-bar warning"><Activity size={17}/>{catalog.problem}</div>}
      {(saving || status?.pending || status?.error) && <div className={'notice-bar ' + (status?.error ? 'warning' : '')} role="status">{status?.error ? <Activity size={17}/> : <LoaderCircle size={17} className="spin"/>}<div><strong>{saving ? '설정을 Git에 저장하고 있어요' : status?.error ? '설정을 적용하지 못했어요' : '저장됨 · 설정 적용 중…'}</strong><span>{status?.error || (saving ? '저장이 끝나면 다시 저장할 수 있습니다.' : 'Fleet이 전달한 설정을 기다리는 중입니다. 계속 편집해도 괜찮아요.')}</span></div>{status?.commit && <code>{status.commit.slice(0, 7)}</code>}</div>}
      <div className="toolbar"><label className="search"><Search size={18}/><input placeholder={view === 'namespaces' ? '네임스페이스 검색…' : '서비스, 도메인, 네임스페이스 검색…'} value={query} onChange={e => setQuery(e.target.value)}/>{query && <button className="icon-button" aria-label="검색 지우기" onClick={() => setQuery('')}><X size={15}/></button>}</label><div className="toolbar-info">{view === 'namespaces' ? `${catalog?.namespaces?.length || 0} namespaces` : `${cards.length} services`}<span className="divider"/><span className="health-dot healthy"/>Ready</div></div>
      {view === 'discovery' && counts.namespaces > 0 && <button className="discovery-callout" onClick={() => move('namespaces')}><FolderTree size={20}/><span><strong>아직 공개 범위를 정하지 않은 네임스페이스 {counts.namespaces}개</strong><small>기본 설정을 확인하기 전까지 관리자에게만 보입니다.</small></span><ArrowUpRight size={19}/></button>}
      {view === 'namespaces' ? <div className="namespace-list">{catalog?.namespaces?.filter(n => n.name.toLowerCase().includes(query.toLowerCase())).sort((a, b) => Number(a.configured) - Number(b.configured) || a.name.localeCompare(b.name)).map(n => <button key={n.name} className="namespace-row" onClick={() => void openEditor({ kind: 'namespace', namespace: n.name })}><span className="namespace-icon"><FolderTree size={20}/></span><div><strong>{n.name}{!n.configured && <span className="new-badge">NEW</span>}</strong><small>{n.services}개 발견 · {n.configured ? policyLabel(n.policy) : '기본 공개 범위 확인 필요'}</small></div><span className="policy-tag">{n.policy.mode === 'public' ? <Globe size={14}/> : <LockKeyhole size={14}/>} {n.policy.mode === 'public' ? '공개' : n.policy.mode === 'admin' ? '관리자' : '제한됨'}</span><Pencil size={15}/></button>)}</div> : <>{(['ingress', 'nodeport', 'custom'] as const).map(source => { const sectionCards = cards.filter(c => c.source === source); if (!sectionCards.length) return null; const Icon = source === 'ingress' ? Globe : source === 'nodeport' ? Network : ArrowDownToLine; return <section className="service-section" key={source}><header className="section-heading"><div><Icon size={18}/><h2>{source === 'ingress' ? 'Ingress' : source === 'nodeport' ? 'NodePort' : 'Custom'}</h2><span className="section-count">{sectionCards.length}</span><span className="section-description">{source === 'ingress' ? '도메인으로 연결' : source === 'nodeport' ? '노드로 직접 연결' : '직접 모아둔 서비스'}</span></div>{source === 'nodeport' && nodeSelect}</header><div className="card-grid">{sectionCards.map(cardView)}</div></section>; })}{!cards.length && <div className="empty-state">{!catalog ? <LoaderCircle size={34} className="spin"/> : view === 'favorites' ? <Star size={34}/> : view === 'discovery' ? <Check size={34}/> : <Layers3 size={34}/>}<h2>{!catalog ? '서비스를 불러오고 있어요' : query ? '검색 결과가 없어요' : view === 'favorites' ? '자주 찾는 서비스를 별표로 모아보세요' : view === 'discovery' ? '모두 확인했어요' : view === 'hidden' ? '숨긴 서비스가 없어요' : '아직 표시할 서비스가 없어요'}</h2><p>{!catalog ? '잠시만 기다려 주세요.' : query ? '다른 이름이나 도메인으로 찾아보세요.' : catalog.identity.admin ? '네임스페이스 공개 범위를 확인하거나 서비스를 직접 추가해 보세요.' : '로그인하면 사용할 수 있는 서비스가 더 있을 수 있어요.'}</p>{catalog && !catalog.identity.subject && <a className="button secondary" href="/auth/login"><LogIn size={16}/>SSO 로그인</a>}</div>}</>}
      <footer className="page-footer"><span><Layers3 size={13}/>KubeShelf <span className="muted">{catalog?.version || 'dev'}</span></span><span>서비스 상태는 Kubernetes의 Pod Ready 기준입니다.</span></footer>
    </main></div>
    {editor && admin && <Editor key={editor.kind === 'card' ? editor.card.id : editor.kind === 'namespace' ? editor.namespace : editor.kind} request={editor} data={admin} saving={saving} onSave={save} onClose={() => setEditor(undefined)}/>}
    {toast && <div role="status" className="toast"><Check size={17}/>{toast}</div>}
  </div>;
}
