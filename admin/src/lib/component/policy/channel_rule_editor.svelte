<script lang="ts">
  export let channels: Array<{ displayName: string; platform: string }> = [];
</script>

<section>
  <h2>Channels</h2>
  <ul>
    {#each channels as channel}
      <li>{channel.platform} · {channel.displayName}</li>
    {/each}
  </ul>
</section>
