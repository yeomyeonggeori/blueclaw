<script lang="ts">
  export let people: Array<{ personID: string; displayName: string; emails: string[]; circles: string[] }> = [];
</script>

<section>
  <h2>People</h2>
  <ul>
    {#each people as person}
      <li>{person.displayName} · {person.emails.join(", ")} · {person.circles?.join(", ") || "member"}</li>
    {/each}
  </ul>
</section>
