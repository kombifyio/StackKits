# Modern Homelab StackKit

> **Status: Public Preview.** The Architecture v2 profile, closed owner
> projections, deterministic generation artifacts, and fail-closed runtime
> admission exist and ship in the public release archives. Live federation and
> complete-kit Apply evidence do not yet exist. Archive availability proves
> self-contained authoring and validation, not production runtime graduation.

Modern Homelab is the hybrid StackKit profile: one StackInstance contains at
least one managed Home Site and at least one managed Cloud Site, joined only by
an explicit, policy-constrained federation bridge. Multi-node by itself is not
Modern Homelab; Basement Kit and Cloud Kit can each contain multiple nodes
inside their own single-environment trust boundary.

The sole normative product definition is `Definition` in
[`stackfile.cue`](stackfile.cue). `stackkit.yaml` contains registry and
read-only migration metadata only. It cannot define topology, services,
placement, PaaS, federation, identity, or execution behavior.

## Required topology and authority

```text
                         public or private clients
                                    |
                         explicit service policy
                                    v
                    +-------------------------------+
                    | Cloud Site                    |
                    | constrained edge + verifier   |
                    | no enrollment/signing power   |
                    +---------------+---------------+
                                    |
                      policy-scoped federation only
                        (initiated from Home Site)
                                    |
                    +---------------v---------------+
                    | Home Site                     |
                    | Control + identity authority  |
                    | device enrollment + data      |
                    +-------------------------------+
```

The default authority boundary is deliberately asymmetric:

- The Home Site owns Control Authority, identity signing, device enrollment,
  and local-data authority.
- The Cloud Site is a default-deny edge and verifier. It cannot enroll devices,
  mint device credentials, or obtain a general route into the home network.
- Connections are initiated from Home and carry only explicitly allowed
  service traffic. Default routes and broad private-subnet advertisements are
  forbidden.
- Cloud loss or bridge loss must leave declared local services available while
  the Cloud edge fails closed.

## Five independent bridge contracts

| Contract | Required decision | Current state |
| --- | --- | --- |
| Connectivity overlay | Exact Home/Cloud peers, Home-outbound initiation, policy-scoped routes, no broad LAN/default route | CUE contract only |
| Service publication | Exact service, hostname, TLS, authentication, origin identity, methods, rate limit, and health contract | CUE contract and resolved projection only |
| Bridge policy | Exact identity-to-service flows and denied authority surfaces | CUE contract only |
| Outbound control channel | Signed, short-lived, capability-scoped actions; never generic shell or SSH | CUE contract only |
| Partition policy | Local autonomy, Cloud fail-closed behavior, stale-verifier expiry, and reconciliation | CUE contract only |

A product may implement several contracts with one technology, but each
contract must remain independently selected, validated, and evidenced. A
provider name, tunnel process, or VPN alone is not the security boundary.

## Current implementation boundary

Implemented today:

- a distinct Modern `KitDefinition` with mandatory Home and Cloud Site kinds;
- Home-only Control Authority and local-only device enrollment constraints;
- explicit federation, publication, data-residency, and partition contracts;
- catalog-bound publication backends and fail-closed readiness blockers;
- owner-specific Home/Cloud identity and federation policy artifacts;
- positive and negative CUE/Go contract tests.

Not implemented today:

- a Modern-specific WireGuard, Pangolin, or other overlay renderer;
- Cloud-edge publication, TLS issuer, origin-verifier, or executable health
  realization;
- an executable signed outbound-control agent or partition reconciler;
- complete Modern OpenTofu/Compose realization, live complete-kit `apply`, or
  live upgrade behavior;
- a dedicated one-line Modern installer or supported compatibility matrix cell;
- a supported Modern PaaS realization. Runtime and workload products are
  selected through independently versioned catalog modules, never by the kit.

The canonical preview input is
[`default-spec.yaml`](default-spec.yaml). It documents the required v2 shape.
Released archives can materialize a fresh native-v2 input with:

```bash
stackkit init modern-homelab --non-interactive --name my-modern-homelab
stackkit validate stack-spec.yaml
```

## Contract validation

These checks validate the definition and fail-closed scaffold; they do not
deploy a Modern Homelab or constitute runtime evidence:

```bash
cue vet ./base/... ./modern-homelab/...
```

Do not run raw `tofu` from `templates/simple`: that directory intentionally has
no deployment template. Generated artifacts must eventually come from the
governed Architecture v2 plan and concrete renderers.

## Related architecture

- [Architecture overview](../docs/ARCHITECTURE.md)
- [Basement Kit](../basement-kit/README.md)
- [Cloud Kit](../cloud-kit/README.md)

HA remains an optional cross-cutting add-on and never a fourth kit. It is not
included in this Preview archive as an independently graduated runtime surface.

## License

Apache-2.0
