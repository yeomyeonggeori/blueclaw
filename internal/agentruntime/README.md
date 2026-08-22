# agentruntime

The tools the model can call, and the identities they run as. Everything the
agent does to the world outside its own reasoning passes through here.

## Model Experience

This package is the whole of what the model sees of this product's
capabilities, and every byte of it is charged on every step of every task.

Nine families, registered in `tool_catalog.go`:

| Family | Registered in |
|---|---|
| terminal | `terminal_tools.go` |
| files and delivery | `file_tools.go` |
| memory | `memory_tools.go` |
| schedules | `schedule_tool.go` |
| skills | `skill_search_tool.go`, `skill_management.go` |
| plan | `plan_tool.go` |
| asking the requester | `ask_tools.go` |
| conversation history | `conversation_history_tool.go` |
| capability operations | `capability_tools.go` |

Capability tools are the exception: their descriptors arrive from `capabilityd`
at runtime, so their wording lives in that service and not in this source.
Everything else is a string literal here.

For the current catalog and what each description costs:

```
tools/model-surface
```

Two things about that surface are worth knowing before changing it. The
descriptions are uneven, from 530 bytes for `schedule_create` down to 47 for
`terminal_run`, and the gap does not track how hard the tool is to use
correctly. And a wrong-tool call is corrected after the fact by recovery
guidance, on every model, every time, so wording that prevents one is cheaper
than it looks.

## Known Limitations and Deferred Work

- No descriptor here says when its tool is the wrong choice. The upstream
  contract carries `WhenToUse` and `WhenNotToUse` fields and none of these fill
  them, so choosing wrongly can only be corrected once it has happened.
- Capability tool wording cannot be reviewed from this repository. A descriptor
  that reaches the model badly worded is a `capabilityd` deploy away from being
  fixed and nothing here fails when it is.
- `capability_tools.go` registers under a runtime name, so `tools/model-surface`
  counts it as one row with no description. The real capability surface is
  whatever the device's catalog holds at the time.
- The tool catalog is assembled per request from exposure rules that live
  upstream, so the list above is what may be exposed rather than what any single
  turn carries.
