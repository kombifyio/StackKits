<script lang="ts">
  import StackKitsPlannerHost from '../../src/StackKitsPlannerHost.svelte'
  const capacityAssetBaseURL = new URL('data/stackkits-capacity/', document.baseURI).href
</script>

<StackKitsPlannerHost sourceSha={__STACKKITS_SOURCE_SHA__} {capacityAssetBaseURL} />
