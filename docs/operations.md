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
  version: 0.1.11
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

The application writes shared settings to the configured manifest path (default `runtime/settings.yaml`):

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

Git working data lives in `emptyDir`. Only one replica is supported, using `Recreate`. Source branches and Git history are authoritative; back up the Git server. The app commits the shared ConfigMap and account ConfigMaps under `runtime/users/`; deployment secrets must never be added through the editor.

## Account settings, favorites and default addresses

Each active directory user gets a separate manifest at `runtime/users/kubeshelf-user-<hash>.yaml`, where `<hash>` is the first 40 hexadecimal characters of SHA-256 of the stable OIDC subject. The ConfigMap has the same name, label `kubeshelf.io/user-settings: "true"`, subject annotation `kubeshelf.io/subject`, and one `favorites.json` data entry. It stores collections, membership, starting collection, `defaultTarget`, and a `defaultEndpoints` map keyed by card ID. The authenticated session determines the file; clients cannot select another subject.

On startup, the authentik directory is loaded and missing account files are initialized in one commit. Later directory additions are checked every minute. Existing profiles are preserved, including disabled accounts. Administrators start with three editable collections; ordinary users have one favorites list. Card IDs retained after access is revoked never grant catalog access. New address preferences must be visible to the saving user; missing or restricted saved references fall back to an available address.

Personal stars, collections and address selectors create a draft. The bottom save bar pushes all personal changes together and locks personal controls until completion. Failed pushes keep the draft. Different accounts edit different files. Shared settings and each account have separate content-version checks; another account's commit does not invalidate an unchanged file. Two devices editing the same account return a conflict instead of silently overwriting. Writes are serialized with a fresh fetch and never force-push. Reads use the last confirmed remote commit, so a rejected local commit cannot appear as saved.

Fleet includes the `users/` YAML files in the existing `runtime` bundle; do not add a nested `fleet.yaml`. Extend the trusted deployment repository's ConfigMap admission allowlist to `^kubeshelf-user-[0-9a-f]{40}$` as well as the shared ConfigMap name. RBAC cannot express name prefixes for update/patch/delete, so combine namespace-scoped permissions with a fail-closed admission policy. Continue restricting Helm metadata Secrets to the dedicated release. The app gets namespace-scoped ConfigMap get/list/watch only and no Secret API permissions. User ConfigMap events refresh its Git snapshot; this is separate from Fleet polling and requires no dynamically mounted per-user volumes.

Upgrading from shared address defaults moves legacy `defaultTarget` and `apps[cardID].defaultEndpoint` into the first configured administrator's account, preserving existing personal choices and removing the old shared fields atomically. Registered NodePort domains and access policies stay shared. The temporary browser-favorites migration shipped in 0.1.10 was removed in 0.1.11 after its transfer was verified in Git and Kubernetes. Anonymous browser favorites remain local.

The view routes `/`, `/favorites`, `/discovery`, `/namespaces`, and `/hidden` serve the SPA directly. Collections use `/favorites?collection=<id>`. Unknown paths and missing assets remain 404. Login accepts only these local routes and the validated collection query as return destinations. Management pages and APIs retain their administrator checks.

## Discovery and visibility

Visibility precedence is address override → original service-card override → default for that namespace and exposure type. `namespaces` holds Ingress policies; `nodePortNamespaces` holds independent NodePort policies. A missing NodePort policy defaults to administrator-only, even when the Ingress policy is public. Existing settings without the new map remain valid and retain Ingress permissions. Restricted policies match any selected group or user. Unconfigured namespaces and manual services default to administrator-only. Namespaces without discovered Ingress or NodePort endpoints are automatically omitted from the namespace list and its review count. Ingress and NodePort policies are reviewed independently when that exposure type exists. Adding a NodePort to an Ingress-only namespace with no NodePort policy marks the namespace for review again. When endpoints are added to an existing namespace, it returns automatically; newly discovered services appear in the discovery inbox. Stored namespace policies and explicit service reviews are retained while the namespace is absent from the list. Presentation grouping never grants access to an otherwise unauthorized address. Hiding is separate from permission policy. Each namespace policy may include `hidden: true` as its dashboard default, independently for Ingress and NodePort. Service settings use `display: "inherit" | "show" | "hide"`; unset values inherit, except legacy `hidden: true` continues to mean explicitly hidden. Existing `hidden: false` values inherit the namespace default. Showing a service never bypasses its access policy. The namespace remains listed with its default-hidden indicator, while the affected services move to the administrator hidden view. Mixed presentation groups filter individual hidden addresses out of dashboard links; newly grouped addresses retain their original namespace defaults. Restore explicitly shows the selected services, without changing their namespace defaults or access permissions.

Ingresses are grouped by namespace, backend Service and resolved Service port. Each concrete host/path is an address choice. Wildcard hosts, hostless/default backends and regex paths require a real URL. Distinct backends can be grouped explicitly within a section. Kubernetes resource UIDs and Pod rollouts do not reset review status; new addresses and changed routing details do.

NodePort URLs use the selected node's advertised address or a registered manual domain. `Local` services use only nodes with Ready, non-terminating Pod-backed endpoints. The browser resolves a manual domain through DNS; the link remains `domain:NodePort`. A manual domain requires an explicit node binding for a Local service. This optional field is under advanced settings and only establishes Ready-Pod eligibility; it does not override or infer DNS. Unbound domains remain usable for non-Local services. Manual TCP diagnostics are also under advanced settings and disabled by default. If no eligible node exists, the link is unavailable. HTTP/HTTPS is inferred from Service port metadata and can be corrected in the editor. Non-web ports offer address copying. An optional, administrator-triggered TCP check is a server-side reachability observation, not a browser or application health check.

Kubernetes API permissions are read-only for Nodes, Namespaces, Services, Pods, Ingresses and EndpointSlices. The app cannot read arbitrary Secrets through that API. Node readiness and Pod readiness are indicators, not a guarantee that a browser can reach a service through its firewall or network.

## Releasing

Update chart `version`, `appVersion`, default image tag and application package version, commit, then push a matching `vX.Y.Z` tag. CI tests the frontend and backend and renders the chart. The release workflow builds both image architectures, publishes the image, then preserves older chart archives while updating the index. The tag workflow archives the chart on `gh-pages`, then dispatches `pages.yml` on `main` to respect GitHub Pages environment protection. Pin a chart version and optionally an image digest in private values. Do not overwrite an existing released version.
