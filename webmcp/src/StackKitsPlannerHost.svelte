<script lang="ts">
  import { onMount } from "svelte";
  import { loadCatalog } from "./catalog.js";
  import { createPlannerSession, setSharedPlannerSession, type PlannerSession } from "./session.js";
  import StackKitsPlanner from "./StackKitsPlanner.svelte";
  import { registerStackKitsWebMcp, type WebMcpRegistration } from "./webmcp.js";

  export let sourceSha: string;
  export let preselectedStackkitId = "";

  let session: PlannerSession | undefined;
  let webMcpAvailable = false;

  onMount(() => {
    const loadController = new AbortController();
    let active = true;
    let registration: WebMcpRegistration | undefined;

    void (async () => {
      try {
        const catalog = await loadCatalog(undefined, loadController.signal);
        if (!active) return;
        session = setSharedPlannerSession(createPlannerSession(catalog, { sourceSha }));
        const candidate = await registerStackKitsWebMcp({
          document,
          session,
          sourceSha,
        });
        if (!active) {
          candidate.dispose();
          return;
        }
        registration = candidate;
        webMcpAvailable = candidate.registered;
      } catch {
        if (active) {
          session = createPlannerSession(undefined, { sourceSha });
          webMcpAvailable = false;
        }
      }
    })();

    return () => {
      active = false;
      loadController.abort();
      registration?.dispose();
    };
  });
</script>

{#if session}
  {#key session}
    <StackKitsPlanner {session} {webMcpAvailable} {preselectedStackkitId} />
  {/key}
{:else}
  <p class="planner-loading" role="status">Loading the CUE authority catalog…</p>
{/if}

<style>
  .planner-loading {
    max-width: 1180px;
    margin: 0 auto;
    padding: 10rem 1.5rem 6rem;
    color: var(--color-on-surface-variant, #a3a3a3);
  }
</style>
