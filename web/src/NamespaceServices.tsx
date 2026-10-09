import { EyeOff, Globe, Network } from 'lucide-react';
import type { Card, Settings } from './types';

export default function NamespaceServices({ namespace, cards, settings }: { namespace: string; cards: Card[]; settings: Settings }) {
  // Cards can combine endpoints from different namespaces; inspect each endpoint.
  const entries = cards.flatMap(card => card.endpoints
    .filter(endpoint => endpoint.namespace === namespace && (endpoint.kind === 'ingress' || endpoint.kind === 'nodeport'))
    .map(endpoint => ({ card, endpoint })))
    .sort((a, b) => a.card.name.localeCompare(b.card.name) || a.endpoint.label.localeCompare(b.endpoint.label) || a.endpoint.id.localeCompare(b.endpoint.id));

  return <section className="namespace-services" aria-label="네임스페이스의 발견된 서비스">
    <h3>발견된 서비스</h3>
    <p className="muted">이 네임스페이스의 접속 항목입니다. 숨긴 서비스도 포함합니다.</p>
    <div className="namespace-service-scroll" role="region" aria-label="Ingress 및 NodePort 목록" tabIndex={0}>
    {(['ingress', 'nodeport'] as const).map(kind => {
      const items = entries.filter(({ endpoint }) => endpoint.kind === kind);
      if (!items.length) return null;
      const Icon = kind === 'ingress' ? Globe : Network;
      return <section className="namespace-service-group" key={kind}>
        <h4><Icon size={14}/>{kind === 'ingress' ? 'Ingress' : 'NodePort'}<span>{items.length}개 {kind === 'ingress' ? '주소' : '포트'}</span></h4>
        <ul>{items.map(({ card, endpoint: e }) => {
          const app = settings.apps[e.appId];
          const override = app?.endpoints?.[e.id]?.visibility || app?.visibility;
          return <li key={e.id}>
            <div className="namespace-service-title"><strong>{card.name}</strong>{card.hidden && <span><EyeOff size={12}/>숨김</span>}{e.local && <span className="local-badge">Local</span>}</div>
            <p className="namespace-service-address">{kind === 'ingress' ? e.url || e.label : `${e.scheme.toUpperCase()} · 노드 주소:${e.nodePort} · ${e.protocol || 'TCP'}`}</p>
            {e.ingresses?.length ? <p className="muted">Ingress: {e.ingresses.join(', ')}</p> : null}
            <p className="muted">Service: {e.service}{e.port ? `:${e.port}` : ''}</p>
            {e.needsURL && <p className="namespace-service-note">접속 URL 직접 지정 필요</p>}
            <p className="namespace-service-note">{override ? `별도 공개 범위 적용 · ${override.mode === 'public' ? '누구나' : override.mode === 'admin' ? '관리자만' : '선택한 그룹 또는 사용자'}` : '네임스페이스 공개 범위 따름'}</p>
          </li>;
        })}</ul>
      </section>;
    })}
    {!entries.length && <p className="muted">현재 발견된 Ingress·NodePort가 없습니다.</p>}
    </div>
  </section>;
}
