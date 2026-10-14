# Network DNS policy

After apply, run `stackkit setup network --owner-approved` to observe the
installed policy through the admitted node-local application API. It uses
existing node password custody; supply no credentials file or onboarding flag.
The action compares the live version, running protection, local resolver and
selected-client settings with the exact applied artifact, then evaluates the
blocked names and an owned-zone name with upstream filtering. A drift or
disabled protection fails the action without rewriting policy. The existing
signed setup receipt describes API policy verification, not household login
or actual client DNS connectivity. `check_host` alone forces filtering, so its
answer is never sufficient without the live-configuration checks.

Read the exact applied Network module and its node custody before observing it.
Set `dns-policy` to explicit private client IPs/prefixes, an existing private
resolver on port 53, and canonical blocked domains. The compiled local zone is
preserved and cannot be blocked. `lan-listener=true` deliberately exposes
TCP/UDP 53535; leaving it absent keeps DNS inside the application network.

Point only a selected test client or an explicit router rule at that endpoint.
Prove its blocked query and an unselected client's allowed answer, over UDP and
TCP, then prove the owned local name still resolves. Check authenticated admin
access and reject unauthorized access. Never change DHCP, the router, a user's
resolver, mesh grants or existing bindings automatically. Upstream application
features and wider network acceptance remain with their existing owners.

DNS policy is declarative: activation regenerates it from the StackSpec. Query
logs/statistics stay off; change policy through the plan, not transient UI edits.

The selected module compute profile allocates CPU/RAM through the same bound
profile used by the native fit/capacity engine. It does not change filtering,
the 20-QPS limiter, cache/log/DHCP settings or the setup action. An allocation
measurement is not physical-device, named high benefit or V1 qualification.
Retained v2 deployments keep their original allocation; new generation uses
the required v3 profile binding.

Select the source addresses the daemon actually sees. Direct clients in one
subnet can have separate IP policies. A NAT/forwarder collapses clients to its
own address; selecting that address selects all of those forwarded clients.
No inferred original identity or automatic router/DHCP change is performed.
