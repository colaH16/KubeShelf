# KubeShelf

A dashboard that discovers Kubernetes Ingress and NodePort services, alongside your own links. One shelf for your cluster, with namespace defaults and per-user or per-group visibility.

![KubeShelf demo](docs/dashboard.png)

## What it does

- Finds Ingress URLs and groups aliases that resolve to the same namespace, Service and port. Override names, icons, URLs, grouping and visibility.
- Offers one NodePort node/domain selector. Services using `externalTrafficPolicy: Local` keep their last eligible node when the selected node has no Ready backend.
- Shows health from Kubernetes Pod Ready and EndpointSlice data. It does not probe web applications. Unknown or unavailable cluster data stays unknown.
- Supports manual services, search, favorites, Git-backed default addresses, copying links, hiding/restoring cards, and an explicit discovery review inbox.
- Shows namespaces only while they have discovered Ingress or NodePort endpoints. Existing namespaces return automatically when endpoints are added; unconfigured ones remain admin-only until reviewed.
- Supports optional visitor login through OIDC and an authentik user/group directory. Administrators are identified by stable OIDC subjects, not mutable display names.
- Supports independent Ingress/NodePort namespace hiding defaults. Services can inherit, explicitly show, or explicitly hide; namespaces stay in their management list with a default-hidden indicator. Hidden addresses remain available in the administrator hidden view.
- Separates Ingress and NodePort namespace permissions. NodePorts default to administrator-only, including in existing public namespaces.
- Filters unauthorized addresses and node information on the server before returning the catalog. This controls dashboard visibility; destination applications still need their own access control.

Namespace visibility dialogs list discovered Ingress names, addresses, backing Services and NodePorts, including hidden services and indicators for visibility overrides. Ingress and NodePort tabs have independent visibility controls. Compact resource lists and help text scroll while visibility controls and Save stay in view. Service and address editors show the inherited visibility, its source, and selected groups/users next to the selector. Grouped addresses keep their original policy sources; the preview follows unsaved edits.

Authenticated users save favorites and default addresses in their own ConfigMap manifests in private Git. Administrators start with three editable collections (Daily, Management, Monitoring); other users have one favorites list. A service may belong to several administrator collections. Choose an optional starting collection. Stars, collection edits and default-address choices are staged until the bottom **Save favorites / default addresses** button is clicked. Personal controls lock during the immediate push; failures retain the draft. Each account only receives addresses allowed by the applied access policies.

Workspace views have direct URLs: `/`, `/favorites`, `/discovery`, `/namespaces`, and `/hidden`. Collections have URLs such as `/favorites?collection=daily`. Refresh, history navigation, and returning to a view after login are supported; management views require administrator access.

## Local demo

Requires Node 24, Go 1.25+ and Git. The demo uses synthetic data and needs no Kubernetes, authentik or Git server.

```sh
cd web
npm ci --ignore-scripts
npm run build
cd ..
KUBESHELF_DEMO=true KUBESHELF_PUBLIC_URL=http://localhost:8080 go run ./cmd/kubeshelf
```

Visit `http://localhost:8080`. The login button opens a demo administrator session. Never enable demo mode on a production deployment.

## Install with Helm

```sh
helm repo add kubeshelf https://colah16.github.io/KubeShelf
helm repo update
helm upgrade --install kubeshelf kubeshelf/kubeshelf \
  --version 0.1.12 --namespace public-services --values private-values.yaml
```

See [example values](examples/values.yaml), [chart defaults](charts/kubeshelf/values.yaml) and the [operations guide](docs/operations.md). Production requires an existing runtime ConfigMap, SSH credentials and pinned host keys, OIDC client credentials, and an authentik directory token. These belong in your private GitOps repository.

The public repository contains application source, a container build and a reusable Helm chart. Tagged releases publish `ghcr.io/colah16/kubeshelf` for AMD64 and ARM64, and update the GitHub Pages Helm repository. Keep actual domains, user IDs, scheduling preferences, secrets and volume definitions outside this repository.

## Configuration lifecycle

```text
Editor → validate → Git commit and immediate push → Fleet reconciliation
       → ConfigMap projection → application reload → applied revision confirmed
```

Saving waits for the push and prevents overlapping saves. In service editors, editing enables Save and disables review. Save persists the edits and acknowledges the displayed addresses together, clearing their NEW/CHANGED markers. With no unsaved changes, review acknowledges the addresses without changing their settings. Both actions are disabled while a request is in flight; failed requests preserve the edits and leave review markers unchanged. Editing can continue while Fleet applies a saved revision. The live catalog and permissions change only after the mounted ConfigMap changes. There is no timed batch of Git pushes or periodic application Git polling. Fleet's polling interval is only one part of propagation time; kubelet projection adds delay.

The application runs as one replica with ephemeral Git working storage. Shared settings and per-account settings are durable in private Git and reconciled by Fleet into Kubernetes ConfigMaps; no database or persistent volume is needed. Shared permissions become active from the mounted ConfigMap. Personal choices are available after a confirmed push, and user-ConfigMap events refresh the Git snapshot when settings change externally. Anonymous preferences remain local to that browser. Sessions are held in memory and require a new login after restart.

## Development

```sh
cd web && npm test && npm run build && cd ..
go test ./...
go vet ./...
helm lint charts/kubeshelf --set demo=true --set publicURL=http://localhost:8080
```

Kubernetes discovery refreshes every 10 seconds; the browser refreshes every 5 seconds. Runtime configuration is reread every second. The implementation currently targets one cluster and an authentik-backed user/group directory.
