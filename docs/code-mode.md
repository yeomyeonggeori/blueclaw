# Does Code Mode fit our boundaries?

DeepSeek Harness lets a model write code that calls tools from inside itself
(`packages/code-runtime`). The reserved `run_code` transport and the sub-calls
the code makes both go through the same tool pipeline: a sub-call carries its
parent's token, logs `tool/code-dispatch`, and returns a denial as a binding
rejection inside the running code.

The reason to want it here is arithmetic. `bluecollar/docs/budgets.md` measured
the model at 85% to 97% of a task's wall clock across 147 runs, so anything that
removes round trips removes almost all of what a task spends.

Part of that is already banked. On the native tool-call path a single response
can carry several calls, which `batchedNativeAgentActions` queues and the loop
runs in order, stopping at the first failure. Ten independent calls already cost
one round trip. What batching cannot express is a call whose input depends on
what the previous one returned, or a loop over a list whose length is only known
at runtime. Those still cost a round trip each, and that is the gap Code Mode
closes.

This document answers the five questions that decide whether the shape fits,
from the code as it stands.

## 1. Approval

`approvalgate.Gate.AwaitApproval` pauses the whole task run
(`PauseTaskRun(..., TaskStatusWaitingApproval, confirmation)`), records the held
call, and returns `ApprovalDecisionHeld`. The task then sits there until a human
answers, which may be days later, and resumes on a fresh turn carrying
`CarriedOutCalls`.

A running script cannot be suspended for days. That leaves three shapes:

1. **The sub-call fails and the script keeps going.** The held call becomes an
   exception the code can catch. The nine things the script already did stay
   done, and the tenth is reported back for the human. This is what dsh means by
   a binding rejection, and it is the only shape that does not lose work.
2. **The script aborts on the first held call.** Simple, and it throws away
   whatever the script did in the eight calls before it. For a script whose
   whole point was to batch mechanical work, that is the expensive case.
3. **Approval is resolved before the script runs.** Not possible: the script
   decides what to call while it runs, which is the reason to have it.

Shape 1 is the only one worth building, and it has a cost the current design
does not have. Today a held call means the task is waiting and the model is not
running. Under Code Mode a held call means the script continues with a hole in
it, so the script's own result has to say which parts happened. That is a
contract the model author has to get right every time.

## 2. Tool exposure

`bluecollar/loop/tool_exposure.go` rebuilds the callable set on every step from
the observations so far, and caps it at 15 tools
(`maxExtensionCallableToolCount`). Exposure is a per-step decision, not a
per-task one.

A script that runs for a while can ask for a tool the loop would not have
exposed at the step it started. Two answers:

- **Freeze at dispatch.** The script may call what was exposed when `run_code`
  was chosen. Predictable, and the ceiling of 15 becomes a real ceiling on what
  a script can reach.
- **Re-evaluate per sub-call.** Matches the loop's own rule and makes a script's
  behaviour depend on state that changed while it ran, which is hard to explain
  to whoever reads the transcript afterwards.

Freezing is the answer that keeps the transcript readable, and it needs the
exposure set recorded on the `run_code` observation so a later reader can tell
what the script was allowed to do.

## 3. The POSIX boundary

This is the question with the clearest answer and it is not the one the shape
wants.

Terminal work runs as the requester's own POSIX identity through
`agentruntime.runRequesterShell`, and file tools are being routed the same way,
so ownership and mode bits decide access rather than any Go-side check. A code
runtime is a second execution world. It has to run as the same actor, with the
same identity, groups, home and umask, or the boundary has two answers.

dsh runs its code in a worker thread inside the harness process. That is not
available here: the harness process is service-owned and reaching a tool from
inside it is exactly what the POSIX boundary exists to prevent. The code would
have to run as an unprivileged process under the requester's identity, calling
back into the runtime over a socket the requester can reach and nobody else can.
That socket is the whole design problem, and it is larger than the code runtime
itself.

## 4. Evidence and the completion gate

`finish` cites `completionEvidence` by `observationID` and `toolName`, and the
completion gate reads those observations. A sub-call has to produce a real
observation with a real ID, or the gate loses the ability to check the work and
the model loses the ability to cite it.

That is tractable. dsh already logs each sub-call as its own pipeline event, and
the loop already records a carried-out call as its own observation
(`recordCarriedOutCalls`). The sub-calls of one `run_code` become observations
whose IDs the script's result names.

What does not survive is one-observation-per-action. A single step would produce
many observations, and everything counting steps, tool calls and repeats
(`tool_repeat_chain`, `attemptFingerprint`, the recovery budget) is written
against the current shape.

## 5. Overlap with the terminal

`terminal_run` already lets the model write a script and run it, as the
requester, with a real machine underneath. What Code Mode adds is that the
script can call *our* tools, with our identity resolution, our approval gate and
our effect recording.

So the honest comparison is not against having no scripting. It is: how much of
the batching win is already available by writing a shell script, and how much
needs the tools? Mechanical file and process work is already scriptable today.
Work that needs `message_send`, `calendar_create` or `task_add` in a loop is
not, and that is where the round trips actually pile up.

## Where this leaves it

The win is narrower than the raw arithmetic suggests, because independent calls
already batch. What is left is dependent and repeated work, and the blockers are
unequal. Exposure freezing and evidence are design work inside shapes that
already exist. The POSIX boundary is a new privileged socket, and the runtime's
whole access story is that there is no such thing.

So the question to answer before building anything is how much dependent work
there actually is. The ledger holds it: every `agent.action` names the tool and
the input, so a pass over recorded runs can count the calls whose input quotes
an earlier observation, against the calls that batched. If that number is small,
Code Mode buys a socket nobody wanted. If it is most of the tool traffic, the
socket is worth designing.
