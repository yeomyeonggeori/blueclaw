<script lang="ts">
  import { onMount } from "svelte";
  import { loadPolicyDocument, type PolicyDocument } from "$lib/api/admin_api_client";
  import CircleStatus from "$lib/component/policy/circle_status.svelte";
  import PeopleEditor from "$lib/component/policy/people_editor.svelte";
  import ChannelRuleEditor from "$lib/component/policy/channel_rule_editor.svelte";
  import RetentionEditor from "$lib/component/policy/retention_editor.svelte";
  import ValidationPanel from "$lib/component/policy/validation_panel.svelte";

  let policyDocument: PolicyDocument = {
    people: [],
    circles: [],
    resourceAccess: [],
    channels: [],
    retention: {
      rawEventDays: 60
    }
  };

  onMount(async () => {
    policyDocument = await loadPolicyDocument();
  });
</script>

<CircleStatus circles={policyDocument.circles} />
<PeopleEditor people={policyDocument.people} />
<ChannelRuleEditor channels={policyDocument.channels} />
<RetentionEditor rawEventDays={policyDocument.retention.rawEventDays} />
<ValidationPanel message="Policy file is loaded from the backend." />
