<script lang="ts">
  export let id: string;
  export let label: string;
  export let unit: string;
  export let value: number | undefined;
  export let minimum: number;
  export let maximum: number;
  export let wholeNumber = false;
  export let onValue: (raw: string) => void;

  const trackSize = 1000;
  $: valid = value !== undefined && Number.isFinite(value) && value > 0 && value <= maximum && (!wholeNumber || Number.isInteger(value));
  $: valueText = value === undefined ? "Not declared" : `${value} ${unit}`;
  $: position = value && value > 0
    ? Math.max(0, Math.min(trackSize, Math.log(value / minimum) / Math.log(maximum / minimum) * trackSize))
    : 0;

  // This only scales the control, never estimates capacity or changes authority.
  // Log spacing leaves useful pointer travel for small homelab values as well
  // as large hosts. The number field remains the exact, unsnapped input.
  function moveSlider(event: Event): void {
    const raw = Number((event.currentTarget as HTMLInputElement).value);
    const scaled = minimum * (maximum / minimum) ** (raw / trackSize);
    const quantum = wholeNumber ? 1 : 0.0625;
    onValue(String(Math.max(minimum, Math.min(maximum, Math.round(scaled / quantum) * quantum))));
  }

  function useKeyboard(event: KeyboardEvent): void {
    const current = valid && value !== undefined ? value : minimum;
    const increment = wholeNumber || current >= 1 ? 1 : 0.0625;
    const changes: Record<string, number> = {
      ArrowRight: increment, ArrowUp: increment,
      ArrowLeft: -increment, ArrowDown: -increment,
      PageUp: increment * 10, PageDown: -increment * 10,
    };
    if (!(event.key in changes) && event.key !== "Home" && event.key !== "End") return;
    event.preventDefault();
    const next = event.key === "Home" ? minimum : event.key === "End" ? maximum : current + changes[event.key];
    onValue(String(Math.max(minimum, Math.min(maximum, next))));
  }
</script>

<div class="capacity-control">
  <label for={id}>{label} <em aria-hidden="true">{value === undefined ? "Not declared" : "Declared"}</em></label>
  <input
    {id} type="number" min={wholeNumber ? 1 : 0} max={maximum} step={wholeNumber ? 1 : "any"}
    value={value ?? ""} oninput={(event) => onValue(event.currentTarget.value)}
    aria-invalid={value !== undefined && !valid ? "true" : undefined}
    aria-describedby={`${id}-state`} data-testid={id}
  />
  <input
    class="capacity-slider" type="range" min="0" max={trackSize} step="1" value={position}
    oninput={moveSlider} onkeydown={useKeyboard}
    aria-label={`${label} slider`} aria-valuetext={valueText}
    aria-valuemin={minimum} aria-valuemax={maximum} aria-valuenow={valid ? value : minimum}
    aria-describedby={`${id}-state`} data-testid={`${id}-slider`}
  />
  <small id={`${id}-state`} data-testid={`${id}-state`} class:invalid={value !== undefined && !valid}>
    {#if value === undefined}Not declared — adjust the slider or enter a number.
    {:else if !valid}Enter {wholeNumber ? "a whole number" : "a number"} greater than zero and at most {maximum}.
    {:else}{valueText} declared.{/if}
  </small>
</div>

<style>
  label { display: block; margin-bottom: .38rem; color: var(--muted); font-size: .76rem; font-weight: 650; }
  em { color: var(--accent); font-size: .65rem; font-style: normal; font-weight: 500; }
  input { box-sizing: border-box; width: 100%; min-height: 2.7rem; border: 1px solid var(--outline); border-radius: .55rem; padding: .65rem .75rem; background: #090909; color: var(--text); font: inherit; }
  input:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .capacity-slider { min-height: 1.5rem; margin-top: .55rem; padding: 0; accent-color: var(--accent); border: 0; background: transparent; }
  small { display: block; margin-top: .35rem; color: var(--muted); font-size: .68rem; line-height: 1.35; }
  .invalid { color: #f87171; }
</style>
