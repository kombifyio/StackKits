<script lang="ts">
  import { formatCommand, formatHandoffMarkdown, type HandoffShell } from "./handoff-export.js";
  import type { PlannerState } from "./session.js";
  import type { WebMcpCatalog } from "./types.js";

  export let state: PlannerState;
  export let provenance: Pick<WebMcpCatalog, "source_sha" | "authority_bundle_sha256" | "catalog_sha256">;
  let shell: HandoffShell = "bash";
  let feedback = "";
  let copiedTarget = "";
  let manualCopy = "";
  let copyRevision = 0;

  $: output = renderOutput(state, provenance, shell);
  $: resetCopy(state, shell);

  function renderOutput(snapshot: PlannerState, source: typeof provenance, targetShell: HandoffShell) {
    if (!snapshot.handoff?.ready) return { markdown: "", commands: [] as string[], error: "" };
    try {
      // Export validates the complete handoff before any individual command is copyable.
      const markdown = formatHandoffMarkdown(snapshot, source, targetShell);
      return { markdown, commands: snapshot.handoff.steps.map((step) => formatCommand(step[1], targetShell)), error: "" };
    } catch {
      return { markdown: "", commands: [] as string[], error: "This handoff cannot be safely formatted. Prepare it again before copying." };
    }
  }

  function resetCopy(_state: PlannerState, _shell: HandoffShell): void {
    copyRevision += 1;
    feedback = "";
    copiedTarget = "";
    manualCopy = "";
  }

  async function copy(text: string, target: string): Promise<void> {
    if (!text || !state.handoff?.ready || output.error) return;
    const revision = ++copyRevision;
    feedback = "";
    copiedTarget = "";
    manualCopy = "";
    try {
      if (!navigator.clipboard?.writeText) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(text);
      if (revision !== copyRevision) return;
      copiedTarget = target;
      feedback = target === "markdown" ? "Agent Markdown copied. Review the approval boundaries before running any commands." : "Copied. Review this step before running it on your target system.";
    } catch {
      if (revision !== copyRevision) return;
      manualCopy = text;
      feedback = "Clipboard access is unavailable. Select and copy the text below.";
    }
  }
</script>

{#if state.handoff}
  <section class="handoff-output" data-ready={state.handoff.ready} data-testid="handoff-result" aria-label="Prepared handoff">
    <header class="handoff-header">
      <div>
        <div class="handoff-title"><h3>Your handoff</h3><span class:ready={state.handoff.ready} class="status">{state.handoff.ready ? "Ready to review" : "Blocked"}</span></div>
        <p>Run one reviewed step at a time, or hand the complete context to your agent.</p>
      </div>
      {#if state.handoff.ready}
        <button class="markdown-action" disabled={Boolean(output.error)} aria-label="Copy agent Markdown" onclick={() => copy(output.markdown, "markdown")}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 8h12v12H8zM16 4H4v12" /></svg>
          {copiedTarget === "markdown" ? "Markdown copied" : "Copy agent Markdown"}
        </button>
      {/if}
    </header>

    {#if state.handoff.blocked_reasons?.length}<p class="warning">Blocked reasons: {state.handoff.blocked_reasons.join(" · ")}</p>{/if}
    {#if output.error}<p class="warning" role="alert">{output.error}</p>{/if}

    {#if state.handoff.ready && !output.error}
      <div class="copy-toolbar">
        <fieldset><legend>Command shell</legend>
          <label><input type="radio" bind:group={shell} value="bash" /> Bash / POSIX</label>
          <label><input type="radio" bind:group={shell} value="powershell" /> PowerShell</label>
        </fieldset>
        <p>Markdown includes your choices, capacity, notices, commands and CUE provenance. Copying does not execute anything.</p>
      </div>

      <ol class="command-list">
        {#each state.handoff.steps as [id, argv, mutation, idempotent, ownerApproval], index}
          <li>
            <div class="command-header">
              <span class="step-number" aria-hidden="true">{index + 1}</span>
              <div class="command-description"><h4>{id}</h4><p>{mutation ? "Writes local files" : "Read only"} · {idempotent ? "Idempotent" : "Not idempotent"} · {ownerApproval ? "Owner approval required" : "No approval required"}</p></div>
              <button aria-label={`Copy ${id} command`} onclick={() => copy(output.commands[index], id)}>
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 8h12v12H8zM16 4H4v12" /></svg>
                {copiedTarget === id ? "Copied" : "Copy command"}
              </button>
            </div>
            <pre aria-label={`${id} command`}><code>{output.commands[index]}</code></pre>
            <details><summary>Machine-readable argv</summary>
              <pre><code>{JSON.stringify(argv, null, 2)}</code></pre>
              <button aria-label={`Copy ${id} argv`} onclick={() => copy(JSON.stringify(argv, null, 2), `${id}-argv`)}>{copiedTarget === `${id}-argv` ? "Copied" : "Copy argv"}</button>
            </details>
          </li>
        {/each}
      </ol>
      <details class="markdown-preview"><summary>Preview agent Markdown</summary><textarea aria-label="Agent Markdown preview" readonly value={output.markdown} rows="12" spellcheck="false"></textarea></details>
    {/if}

    {#if feedback}<p class="copy-feedback" class:warning={Boolean(manualCopy)} role="status">{feedback}</p>{/if}
    {#if manualCopy}<label class="manual-copy"><span>Copy manually</span><textarea readonly value={manualCopy} onfocus={(event) => event.currentTarget.select()} rows="8" spellcheck="false"></textarea></label>{/if}

    <aside class="apply-boundary"><strong>{state.handoff.apply_follow_up.id}</strong><span>Not included in copied commands. Run target compatibility checks and obtain explicit owner approval separately.</span></aside>
  </section>
{/if}

<style>
  .handoff-output { margin-top: 1rem; border: 1px solid var(--outline); border-radius: .8rem; padding: 1.1rem; background: rgb(0 0 0 / .18); }
  .handoff-header, .handoff-title, .command-header, .copy-toolbar { display: flex; align-items: center; gap: .8rem; }
  .handoff-header { align-items: flex-start; justify-content: space-between; }
  h3, h4, p { margin: 0; }
  h3 { font-size: 1rem; }
  h4 { font: 650 .85rem/1.4 ui-monospace, monospace; overflow-wrap: anywhere; }
  .handoff-header p, .copy-toolbar p { margin-top: .4rem; max-width: 64ch; color: var(--muted); font-size: .78rem; line-height: 1.5; }
  .status { color: #fca5a5; font-size: .7rem; font-weight: 650; }
  .status.ready { color: #86efac; }
  button { display: inline-flex; align-items: center; justify-content: center; gap: .4rem; flex-shrink: 0; min-height: 2.3rem; border: 1px solid var(--outline); border-radius: .45rem; padding: .45rem .65rem; background: var(--surface-high); color: var(--text); font-family: inherit; font-size: .75rem; font-weight: 600; line-height: 1.3; cursor: pointer; transition: border-color 140ms, background 140ms; }
  button:hover { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 9%, var(--surface-high)); }
  button:focus-visible, summary:focus-visible, textarea:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
  button:disabled { opacity: .45; cursor: not-allowed; }
  button svg { width: 1rem; height: 1rem; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linejoin: round; }
  .markdown-action { border-color: var(--accent); color: var(--accent); }
  .copy-toolbar { align-items: flex-start; justify-content: space-between; border-top: 1px solid var(--outline); margin-top: 1rem; padding-top: .9rem; }
  fieldset { display: flex; flex-shrink: 0; gap: .8rem; border: 0; margin: 0; padding: 0; }
  legend { color: var(--muted); font-size: .68rem; margin-bottom: .45rem; }
  fieldset label { display: flex; align-items: center; gap: .3rem; cursor: pointer; font-size: .78rem; }
  input { accent-color: var(--accent); }
  .copy-toolbar p { margin: 0; font-size: .72rem; max-width: 48ch; }
  .command-list { list-style: none; margin: 1rem 0 0; padding: 0; }
  .command-list li { border-top: 1px solid var(--outline); padding: 1rem 0; }
  .step-number { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border: 1px solid var(--outline); border-radius: 50%; color: var(--muted); font: .7rem ui-monospace, monospace; flex-shrink: 0; }
  .command-description { flex: 1; min-width: 0; }
  .command-description p { color: var(--muted); font-size: .68rem; line-height: 1.5; margin-top: .25rem; }
  pre { margin: .75rem 0 0; padding: .9rem; border-radius: .45rem; background: #090909; border: 1px solid var(--outline); white-space: pre-wrap; overflow-wrap: anywhere; overflow: auto; color: var(--text); font: .76rem/1.65 ui-monospace, SFMono-Regular, monospace; tab-size: 2; }
  code { font: inherit; }
  details { margin-top: .65rem; }
  summary { color: var(--muted); font-size: .7rem; cursor: pointer; width: fit-content; }
  .markdown-preview { margin: 0 0 1rem; }
  details button { margin-top: .55rem; }
  .copy-feedback { color: #86efac; font-size: .78rem; line-height: 1.5; margin: .8rem 0; }
  .warning { color: #fde68a; font-size: .78rem; line-height: 1.5; margin: .8rem 0; }
  .manual-copy { display: block; font-size: .78rem; margin: .8rem 0; }
  textarea { box-sizing: border-box; display: block; width: 100%; resize: vertical; padding: .8rem; margin-top: .5rem; background: #090909; color: var(--text); border: 1px solid var(--outline); border-radius: .45rem; font: .75rem/1.5 ui-monospace, monospace; }
  .apply-boundary { display: flex; align-items: flex-start; gap: 1rem; padding: .8rem; border: 1px dashed #f87171; border-radius: .5rem; color: #fecaca; font-size: .73rem; line-height: 1.5; }
  .apply-boundary strong { flex-shrink: 0; }
  @media (max-width: 680px) { .handoff-header, .copy-toolbar, .apply-boundary { flex-direction: column; align-items: stretch; } .command-header { flex-wrap: wrap; } .command-header button { margin-left: 2.3rem; } .handoff-output { padding: .85rem; } }
  @media (prefers-reduced-motion: reduce) { button { transition: none; } }
</style>
