<script lang="ts">
  type Circle = {
    circleID: string;
    displayName: string;
    mattermostChannelID: string;
    workspaceDirectoryPath: string;
  };

  export let circles: Circle[] = [];
</script>

<section>
  <h2>Circles</h2>
  <ul>
    {#each circles as circle}
      <li>
        <strong>{circle.displayName || circle.circleID}</strong>
        · {circle.workspaceDirectoryPath || `/workspace/circles/${circle.circleID}`}
      </li>
    {/each}
  </ul>
</section>
