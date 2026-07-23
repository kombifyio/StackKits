# Product Runtime composition

`pkg/productruntime` is the public, provider-free construction and prepared-
Apply boundary for authenticated StackKits Product Runtime integrations. It
projects the StackKits-owned static owner catalog and selected-PaaS selector,
then consumes the canonical `kombify-go-common` execution-channel,
Apply-evidence Collector, Journal, and opaque recovery-custody contracts.

`NewComposition` fixes the exact remote-only owner allowlist, root identity,
channel authority, Collector, Journal, and Recovery store before resolution.
`ApplyPrepared` and `ReconcilePrepared` then accept only an authenticated
authority scope, workspace, current StackSpec/Inventory, and (for recovery) an
exact request digest. StackKits re-resolves through its embedded CUE authority,
requires byte-identical persisted plan and generated artifacts, acquires the
held output lock, collects evidence through the construction-owned Collector,
and returns only a hash-bound provider-neutral result.
Partial durable execution returns a public `ReconcileRequiredError` containing
only the opaque request digest accepted by `ReconcilePrepared`; child operation
state and provider-native receipts remain private to the owning service.

The API cannot accept caller evidence or an implicit local execution channel.
It does not construct local Operations, select an endpoint, carry credentials,
or own provider lifecycle, leases, generation, discovery, transport, retries,
or persistence. Consumers implement those concerns behind the shared
interfaces and must not import `internal/architecturev2`.

Focused contract checks stay separate and bounded:

```bash
go test ./pkg/productruntime -count=1
cd pkg/productruntime/testdata/externalconsumer
GOWORK=off go test ./... -count=1
```
