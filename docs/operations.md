# Operations

## Public chart, private deployment

A Helm repository is an HTTPS location serving `index.yaml` and chart `.tgz` archives. A GitHub source repository URL alone is not a Helm repository URL. KubeShelf's release workflow generates these files and publishes them through GitHub Pages:

```yaml
# fleet.yaml in your private deployment repository
name: kubeshelf-app
defaultNamespace: public-services
helm:
  releaseName: kubeshelf
  repo: https://colah16.github.io/KubeShelf
  chart: kubeshelf
  version: 0.1.9
  valuesFiles:
    - values.yaml
```

`values.yaml` and encrypted credentials remain private. Static PVs or hostPath resources for other applications can stay in the same private deployment repository; KubeShelf itself does not require them.

For the first release, set GitHub repository **Settings → Pages → Source → GitHub Actions**. After the image first appears, set the GHCR package visibility to Public if anonymous cluster pulls are desired. Only the reusable chart and image are published, not your operating values.

## Required existing resources

The chart references these resources; it does not generate or own runtime settings:

| Resource | Default name | Contents |
| --- | --- | --- |
| ConfigMap | `kubeshelf-settings` | `settings.json` |
| Secret | `kubeshelf-config-git` | `ssh-privatekey` (Ed25519 works) |
| ConfigMap | `kubeshelf-git-known-hosts` | `known_hosts`, verified from the Git server |
| Secret | `kubeshelf-auth` | `client-secret`, `directory-token` |

Mount complete directories, without `subPath`. Secret or ConfigMap file changes can propagate without a rollout. Changes to Helm values supplied as environment variables require a rollout. Kubelet projection is eventually consistent, so a 15-second Fleet poll is not a 15-second end-to-end guarantee.

Create a dedicated OIDC authorization-code client with PKCE, exact callback `https://your-app-host/auth/callback`, `openid profile email` scopes and stable user UUID subjects. Use a dedicated authentik directory service account with only `view_user` and `view_group`; it has no need to create users, change groups or read credentials. KubeShelf refreshes group membership every 30 seconds and fails closed for authenticated permissions when directory verification fails.

If a proxy/CDN forces caching despite origin headers, configure a bypass for the entire application hostname, including `/auth/*` and `/api/*`. KubeShelf sends `private, no-store` and CDN no-store headers. No shared cache should cache authenticated API responses or login callbacks.

## Runtime settings repository

The application writes exactly the configured manifest path (default `runtime/settings.yaml`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubeshelf-settings
  namespace: public-services
data:
  settings.json: |
    {
      "schemaVersion": 1,
      "revision": "initial",
      "namespaces": {},
      "nodePortNamespaces": {},
      "apps": {},
      "manual": [],
      "targets": [],
      "assignments": {}
    }
```

Manage its Fleet `GitRepo` and narrow deployer permissions from a separate, trusted deployment repository. Do not give this writable settings repository general cluster deployment privileges. Fleet's Helm deployment also needs its own release metadata storage permission; scope it to that release rather than all application Secrets. Namespace-scoped Fleet `Policy` restrictions need an isolated Fleet workspace to avoid affecting unrelated GitRepos.

Configure Fleet with `pollingInterval: 15s`. Runtime `fleet.yaml` can use a fixed Helm release name `kubeshelf-runtime`. The settings file contains a revision token, so a newer save can supersede an intermediate pending revision. Stale editor commits and concurrent Git changes return a conflict rather than force-pushing. A push failure leaves the applied configuration untouched. Invalid mounted settings retain the previous valid configuration and display an error to administrators.

Git working data lives in `emptyDir`. Only one replica is supported, using `Recreate`. Source branches and Git history are authoritative; back up the Git server. The app commits only the runtime ConfigMap; deployment secrets must never be added through the editor.

## Saved address defaults and navigation

Optional `defaultTarget` at the settings root stores a NodePort target ID (a discovered node or registered domain). Optional `apps[cardID].defaultEndpoint` stores the default discovered/manual address ID for that presentation card. Older settings work without either field. Unknown, deleted, hidden or unauthorized references cannot expose new targets or addresses; the UI falls back to an available choice.

Administrators stage dashboard selector changes and save them together from the bottom bar. Saving fetches the latest desired settings, merges only the changed defaults, then uses the usual base-commit conflict check and immediate Git push. It also acknowledges the displayed addresses of cards whose default address was changed. The selectors are locked from the initial fetch through push completion. Failed saves preserve the staged choices; successful pushes remove the bar and retain the chosen defaults while Fleet is pending. Applied permissions still come exclusively from the mounted configuration. A new browser uses applied defaults; non-admin visitors can override them locally without writing to Git.

The view routes `/`, `/favorites`, `/discovery`, `/namespaces`, and `/hidden` serve the SPA directly. Unknown paths and missing assets remain 404. Login accepts only these local routes as return destinations. Management pages and APIs retain their administrator checks.

## Discovery and visibility

Visibility precedence is address override → original service-card override → default for that namespace and exposure type. `namespaces` holds Ingress policies; `nodePortNamespaces` holds independent NodePort policies. A missing NodePort policy defaults to administrator-only, even when the Ingress policy is public. Existing settings without the new map remain valid and retain Ingress permissions. Restricted policies match any selected group or user. Unconfigured namespaces and manual services default to administrator-only. Namespaces without discovered Ingress or NodePort endpoints are automatically omitted from the namespace list and its review count. Ingress and NodePort policies are reviewed independently when that exposure type exists. Adding a NodePort to an Ingress-only namespace with no NodePort policy marks the namespace for review again. When endpoints are added to an existing namespace, it returns automatically; newly discovered services appear in the discovery inbox. Stored namespace policies and explicit service reviews are retained while the namespace is absent from the list. Presentation grouping never grants access to an otherwise unauthorized address. Hiding is separate from permission policy. Each namespace policy may include `hidden: true` as its dashboard default, independently for Ingress and NodePort. Service settings use `display: "inherit" | "show" | "hide"`; unset values inherit, except legacy `hidden: true` continues to mean explicitly hidden. Existing `hidden: false` values inherit the namespace default. Showing a service never bypasses its access policy. The namespace remains listed with its default-hidden indicator, while the affected services move to the administrator hidden view. Mixed presentation groups filter individual hidden addresses out of dashboard links; newly grouped addresses retain their original namespace defaults. Restore explicitly shows the selected services, without changing their namespace defaults or access permissions.

Ingresses are grouped by namespace, backend Service and resolved Service port. Each concrete host/path is an address choice. Wildcard hosts, hostless/default backends and regex paths require a real URL. Distinct backends can be grouped explicitly within a section. Kubernetes resource UIDs and Pod rollouts do not reset review status; new addresses and changed routing details do.

NodePort URLs use the selected node's advertised address or a registered manual domain. `Local` services use only nodes with Ready, non-terminating Pod-backed endpoints. The browser resolves a manual domain through DNS; the link remains `domain:NodePort`. A manual domain requires an explicit node binding for a Local service. This optional field is under advanced settings and only establishes Ready-Pod eligibility; it does not override or infer DNS. Unbound domains remain usable for non-Local services. Manual TCP diagnostics are also under advanced settings and disabled by default. If no eligible node exists, the link is unavailable. HTTP/HTTPS is inferred from Service port metadata and can be corrected in the editor. Non-web ports offer address copying. An optional, administrator-triggered TCP check is a server-side reachability observation, not a browser or application health check.

Kubernetes API permissions are read-only for Nodes, Namespaces, Services, Pods, Ingresses and EndpointSlices. The app cannot read arbitrary Secrets through that API. Node readiness and Pod readiness are indicators, not a guarantee that a browser can reach a service through its firewall or network.

## Releasing

Update chart `version`, `appVersion`, default image tag and application package version, commit, then push a matching `vX.Y.Z` tag. CI tests the frontend and backend and renders the chart. The release workflow builds both image architectures, publishes the image, then preserves older chart archives while updating the index. The tag workflow archives the chart on `gh-pages`, then dispatches `pages.yml` on `main` to respect GitHub Pages environment protection. Pin a chart version and optionally an image digest in private values. Do not overwrite an existing released version.
