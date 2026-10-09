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
  version: 0.1.0
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
      "apps": {},
      "manual": [],
      "targets": [],
      "assignments": {}
    }
```

Manage its Fleet `GitRepo` and narrow deployer permissions from a separate, trusted deployment repository. Do not give this writable settings repository general cluster deployment privileges. Fleet's Helm deployment also needs its own release metadata storage permission; scope it to that release rather than all application Secrets. Namespace-scoped Fleet `Policy` restrictions need an isolated Fleet workspace to avoid affecting unrelated GitRepos.

Configure Fleet with `pollingInterval: 15s`. Runtime `fleet.yaml` can use a fixed Helm release name `kubeshelf-runtime`. The settings file contains a revision token, so a newer save can supersede an intermediate pending revision. Stale editor commits and concurrent Git changes return a conflict rather than force-pushing. A push failure leaves the applied configuration untouched. Invalid mounted settings retain the previous valid configuration and display an error to administrators.

Git working data lives in `emptyDir`. Only one replica is supported, using `Recreate`. Source branches and Git history are authoritative; back up the Git server. The app commits only the runtime ConfigMap; deployment secrets must never be added through the editor.

## Discovery and visibility

Visibility precedence is address override → original service-card override → namespace default. Restricted policies match any selected group or user. Unconfigured namespaces and manual services default to administrator-only. Presentation grouping never grants access to an otherwise unauthorized address. Hiding is separate from permission policy.

Ingresses are grouped by namespace, backend Service and resolved Service port. Each concrete host/path is an address choice. Wildcard hosts, hostless/default backends and regex paths require a real URL. Distinct backends can be grouped explicitly within a section. Kubernetes resource UIDs and Pod rollouts do not reset review status; new addresses and changed routing details do.

NodePort URLs use the selected node's advertised address or a registered manual domain. `Local` services use only nodes with Ready, non-terminating Pod-backed endpoints. A manual domain requires an explicit node binding for a Local service. If no eligible node exists, the link is unavailable. HTTP/HTTPS is inferred from Service port metadata and can be corrected in the editor. Non-web ports offer address copying. An optional, administrator-triggered TCP check is a server-side reachability observation, not a browser or application health check.

Kubernetes API permissions are read-only for Nodes, Namespaces, Services, Pods, Ingresses and EndpointSlices. The app cannot read arbitrary Secrets through that API. Node readiness and Pod readiness are indicators, not a guarantee that a browser can reach a service through its firewall or network.

## Releasing

Update chart `version`, `appVersion`, default image tag and application package version, commit, then push a matching `vX.Y.Z` tag. CI tests the frontend and backend and renders the chart. The release workflow builds both image architectures, publishes the image, then preserves older chart archives while updating the index. Pin a chart version and optionally an image digest in private values. Do not overwrite an existing released version.
