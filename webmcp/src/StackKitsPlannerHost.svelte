<script lang="ts">
  import { onMount } from "svelte";
  import { CATALOG_PATH, loadCatalog } from "./catalog.js";
  import { createPlannerSession, setSharedPlannerSession, type PlannerSession } from "./session.js";
  import StackKitsPlanner from "./StackKitsPlanner.svelte";
  import {
    createWebMcpStatus,
    registerStackKitsWebMcp,
    webMcpStatusForCatalogError,
    type WebMcpRegistration,
    type WebMcpStatus,
  } from "./webmcp.js";

  export let sourceSha: string;
  export let preselectedStackkitId = "";

  let session: PlannerSession | undefined;
  let webMcpAvailable = false;
  let webMcpStatus: WebMcpStatus = createWebMcpStatus("checking");
  let retryWebMcp: () => void = () => {};

  onMount(() => {
    let active = true;
    let attempt = 0;
    let loadController: AbortController | undefined;
    let registration: WebMcpRegistration | undefined;

    const start = async (): Promise<void> => {
      const currentAttempt = ++attempt;
      loadController?.abort();
      registration?.dispose();
      registration = undefined;
      const controller = new AbortController();
      loadController = controller;
      session = undefined;
      webMcpAvailable = false;
      webMcpStatus = createWebMcpStatus("checking");
      try {
        const catalogURL = new URL(CATALOG_PATH, document.baseURI);
        if (sourceSha) catalogURL.searchParams.set("source_sha", sourceSha);
        const catalog = await loadCatalog(
          (_input, init) => fetch(catalogURL, init),
          controller.signal,
        );
        if (!active || currentAttempt !== attempt) return;
        if (sourceSha && catalog.source_sha !== sourceSha) {
          session = createPlannerSession(undefined, { sourceSha });
          webMcpAvailable = false;
          webMcpStatus = createWebMcpStatus("catalog_source_mismatch");
          return;
        }
        const nextSession = setSharedPlannerSession(createPlannerSession(catalog, { sourceSha }));
        session = nextSession;
        webMcpStatus = createWebMcpStatus("checking");
        const candidate = await registerStackKitsWebMcp({
          document,
          session: nextSession,
          sourceSha,
        });
        if (!active || currentAttempt !== attempt) {
          candidate.dispose();
          return;
        }
        registration = candidate;
        webMcpAvailable = candidate.registered;
        webMcpStatus = candidate.status;
      } catch (error) {
        if (!active || currentAttempt !== attempt) return;
        session = createPlannerSession(undefined, { sourceSha });
        webMcpAvailable = false;
        webMcpStatus = webMcpStatusForCatalogError(error);
      }
    };

    retryWebMcp = () => {
      if (active) void start();
    };
    void start();

    return () => {
      active = false;
      attempt += 1;
      loadController?.abort();
      registration?.dispose();
      retryWebMcp = () => {};
    };
  });
</script>

{#if session}
  {#key session}
    <StackKitsPlanner {session} {webMcpAvailable} {webMcpStatus} onWebMcpRetry={retryWebMcp} {preselectedStackkitId} />
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
