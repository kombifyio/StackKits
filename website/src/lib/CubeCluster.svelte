<script lang="ts">
  type Props = {
    size?: number
    opacity?: number
    class?: string
    variant?: 'cluster' | 'row' | 'pyramid'
  }
  const { size = 240, opacity = 1, class: className = '', variant = 'cluster' }: Props = $props()

  // Single isometric cube — three rhombus faces. Width:height ratio 1:0.866.
  // The pieces are rendered back-to-front so depth reads correctly.
  type Cube = { x: number; y: number; scale: number; tone: 'light' | 'mid' | 'dark' }

  const clusterCubes: Cube[] = [
    // back row (smaller, deeper orange)
    { x: 0, y: 0, scale: 1, tone: 'dark' },
    { x: 1.0, y: -0.5, scale: 1, tone: 'mid' },
    { x: 2.0, y: 0, scale: 1, tone: 'dark' },
    // middle row
    { x: 0.5, y: 0.55, scale: 1, tone: 'mid' },
    { x: 1.5, y: 0.55, scale: 1, tone: 'light' },
    // front row (largest, brightest)
    { x: 1.0, y: 1.15, scale: 1.05, tone: 'light' },
  ]

  const rowCubes: Cube[] = [
    { x: 0, y: 0, scale: 1, tone: 'dark' },
    { x: 1, y: 0, scale: 1, tone: 'mid' },
    { x: 2, y: 0, scale: 1, tone: 'light' },
  ]

  const pyramidCubes: Cube[] = [
    { x: 0, y: 0, scale: 1, tone: 'dark' },
    { x: 1, y: 0, scale: 1, tone: 'dark' },
    { x: 2, y: 0, scale: 1, tone: 'dark' },
    { x: 0.5, y: 0.55, scale: 1, tone: 'mid' },
    { x: 1.5, y: 0.55, scale: 1, tone: 'mid' },
    { x: 1.0, y: 1.15, scale: 1.05, tone: 'light' },
  ]

  const cubes = $derived(
    variant === 'row' ? rowCubes : variant === 'pyramid' ? pyramidCubes : clusterCubes,
  )

  // Tone palette taken from the kombifyKits cube cluster — top is bright,
  // middle gets the brand primary, bottom shadows pull toward orange-700.
  const tones: Record<'light' | 'mid' | 'dark', { top: string; left: string; right: string }> = {
    light: { top: '#fdba74', left: '#fb923c', right: '#ea580c' },
    mid:   { top: '#fb923c', left: '#f97316', right: '#c2410c' },
    dark:  { top: '#f97316', left: '#ea580c', right: '#9a3412' },
  }
</script>

<svg
  viewBox="-0.5 -1 4 4"
  preserveAspectRatio="xMidYMid meet"
  width={size}
  height={size}
  class={className}
  aria-hidden="true"
  style:opacity={opacity}
>
  {#each cubes as cube}
    {@const { top, left, right } = tones[cube.tone]}
    {@const s = cube.scale}
    {@const cx = cube.x}
    {@const cy = cube.y}
    <!-- One isometric cube positioned at (cx, cy). Each cube is one unit wide,
         0.5 units half-height. We translate via the polygon points. -->
    <g transform={`translate(${cx} ${cy}) scale(${s})`}>
      <!-- top face -->
      <polygon points="0,0 0.5,-0.29 1,0 0.5,0.29" fill={top} />
      <!-- left face -->
      <polygon points="0,0 0.5,0.29 0.5,0.87 0,0.58" fill={left} />
      <!-- right face -->
      <polygon points="1,0 0.5,0.29 0.5,0.87 1,0.58" fill={right} />
    </g>
  {/each}
</svg>
