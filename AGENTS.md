# AGENTS.md

These are the conventions this codebase holds itself to. Apply them to new code,
and apply them to code you touch wherever that does not break consistency with
its surroundings.

## Core Principles

1. **Readability is the highest priority** - code should be self-explanatory
2. **Functional style** - prefer pure functions, avoid side effects
3. **Efficiency** - no redundant operations
4. **Simplicity** - minimal code that solves the problem

## Single Source of Truth

- A shared vocabulary or contract (emoji names, enum options, capability
  names, component lists) consumed by more than one role, package, or
  service is defined exactly once; every consumer derives from that
  definition. Hand-maintained parallel copies are a defect: they drift
  (one list gains a value the other never sees) and every addition
  requires touching N places.
- When a consumer lives in another language and cannot import the
  definition, add a conformance test that reads the canonical source and
  fails on drift, instead of keeping a second hand-edited copy
  (`chatd/tests/buzz-adapter.test.ts` reads
  `.dependency/bluecollar/agentcontract/reaction_emoji.go`).
- On discovering duplicated sources of truth, merge them as part of the
  change that touched them; do not extend a duplicate.

## Working on this repository

Nothing lands on `main` by direct push. Branch, open a pull request, let the
check run, merge.

### Branch names

`<type>/<subject-in-kebab-case>` — the type is the same word the commit will
carry, and the subject says what changes, not what you did to it.

```
feat/acp-context-injection
fix/approval-resume-after-restart
docs/tui-screenshots
refactor/turn-runner-split
test/completion-gate-evidence
chore/gofmt
ci/skip-docs-only-runs
```

Branch off `main`, rebase rather than merge when `main` moves under you, and
delete the branch once the pull request is merged.

### Commit messages

```
<type>: <what changes, imperative, lowercase, no trailing period>

<why it changes: the problem the reader would otherwise have to reconstruct.
Wrap at 72 columns. Say what you deliberately did not do.>
```

`type` is one of `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`. A
scope is allowed when it disambiguates (`fix(acp): …`) and omitted when it does
not.

The subject line is a claim about the code, not a description of the work:
`fix: stop reading "the agent stopped talking" as "the task is done"` rather
than `fix: fixed the status bug`. The body exists to answer *why*; a commit
whose body only repeats the subject should not have one.

### Prose in documents

The README and the docs are read by people deciding whether to trust this
thing. Prose that reads as machine-written costs that trust, and it drifts back
in every time someone lets a model write a paragraph. Grep for it before
committing.

- **One negative-parallel construction per 500 words.** `X, not Y` ·
  `rather than` · `not merely … but`. Keep the ones where the alternative is
  what a reader would actually assume; cut the rest. They stop registering when
  they repeat, which wastes the ones that matter.
- **Under three em dashes per 500 words.** An em dash that bolts an appositive
  onto a finished sentence should be a period.
- **No sentence praising the document's own honesty.** "worth stating plainly",
  "to be clear", "honest list". Be plain and say nothing about it.
- **No section-closing restatement.** If the last sentence of a section adds no
  fact, delete it.
- **Three or more `A X is a Y that …` in a row is a definition list.**
- **A fact stated in a table is not restated in prose.**
- **Bold whole blocks, never words inside a sentence.**

Check with:

```bash
python3 - <<'EOF'
import re, pathlib
text = pathlib.Path("README.md").read_text()
words = len(text.split())
negations = len(re.findall(r", not |rather than |not merely", text))
print(f"{words} words · {negations} negations (1 per {words // max(negations, 1)}) · {text.count(chr(8212))} em dashes")
EOF
```

### Screenshots

The terminal user interface images the README embeds are generated, never
hand-shot. Re-shoot them from the working tree after any change to
`internal/tui`:

```bash
./tools/shoot-tui-screenshots            # every frame, into assets/screenshots/
./tools/shoot-tui-screenshots --only tui-tasks
```

It builds `cmd/blueclaw-cli`, serves a seeded admin API on 127.0.0.1:8099,
drives the client through a pseudo terminal, and renders each screen with
headless Chrome. The demo task runs, ledger, and harness answers live in
`tools/tui_screenshots/fixture_api.py`; change them there so every frame stays
consistent.

### Pull requests

One reviewable change per pull request. The description says what the reader
should look at and what evidence exists that it works — the test that fails
without the change, the scenario that was run, the screenshot. A pull request
that touches unrelated files should be split.

Branch names, commit messages, pull request titles and pull request
descriptions are written in English. Discussion in review can be in whatever
language the reviewers share; the repository's permanent record is English.

## Testing

- `go test ./...` covers the unit suites beside their sources and the
  integration suite under `tests/`. Nothing needs an external service; the
  standalone boot check skips unless `BLUECLAW_TEST_POSTGRES_URL` names a
  database, which a developer supplies locally.
- TypeScript suites are per package: `bun run test` at the repository root
  runs every package suite in its own process, and `cd <package> && bun run
  test` runs one. Never run bare `bun test` at the repository root — it
  blends isolated package processes into one and reports false failures.

- A guard is proven by watching it fail. Write the test, break the thing it
  claims to protect, watch it go red, put the thing back. A test that was
  never seen to fail is an assumption with a green checkmark on it, and the
  ones that matter most here are exactly the shape that can pass for the
  wrong reason. `TestOverheardMessageNeverBecomesTheInstruction` guards the
  behaviour that once had the bot reposting somebody else's announcement as
  its own instruction, and it is worth nothing unless it actually goes red
  when the overheard text reaches the prompt again.
- The pull request says what was broken to prove it. One line, naming the
  edit or the revision, is enough. Anyone reading the guard later can redo
  it from that line, which is the point.

## Which revision a binary is

`blueclaw --version` answers it and `GET /admin/api/harness` carries it, so a
deploy can read back what landed instead of trusting a green exit code.

The answer comes from `-ldflags`, and it has to, because this repository is
usually built as a submodule of the host: its `.git` is a gitdir file pointing
outside the tree, so the Go toolchain records no VCS information at all and
`debug.ReadBuildInfo` has nothing to report. A build that wants an answer passes

```
-ldflags "-X github.com/yeomyeonggeori/blueclaw/internal/buildrevision.injected=$(git rev-parse HEAD)"
```

A build that passes nothing reports `unknown`, which is the honest answer and
the one a deploy check should refuse.

## LLM-First Runtime Policy

- User-facing answers, failure explanations, approval wording, and recovery direction must go through the LLM.
- Deterministic runtime code may validate, normalize, enforce schemas, orchestrate retries, and record diagnostics, but must not compose fallback sentences for users.
- Exact control acknowledgements for slash commands, such as stop/stop-all, may use deterministic system responses; do not expand that exception to task judgment, failure explanation, recovery direction, or confirmation wording.
- When a failure requires a judgment, request structured output first, then use that structured decision as input to an LLM-generated user reply.
- Deterministic helpers may prepare safe facts for the model, such as failure stage, error code, known context, and attempted actions.
- For real task failures, do not fully suppress the user reply. Try local LLM failure wording first, then send a compact raw error summary if no LLM path can produce a usable notice.
- Full suppression is only for intentionally ignored control/runtime cases such as duplicate delivery, cancelled task output, or self/bot messages.

## Code Style

### No Comments
Code self-documents through descriptive names and small functions; write zero comments. The only comment that survives names a concrete external fact — an upstream bug with its number, a spec with its clause, a provider behavior with its documented source. Rationale is never that: a sentence explaining why the change was made, what the code is for, or what would break without it belongs in the commit message, never in the file.

### No Abbreviations
Use full names: `response` not `res`, `error` not `err`, `configuration` not `config`

### Initialism Casing (camelCase)
- **Leading**: lowercase (`idToken`, `urlParams`, `apiKey`)
- **Trailing**: UPPERCASE (`userID`, `callbackURL`, `oauthAPI`)

### Naming Conventions
- **Functions**: Clear verbs (`calculateTotalPrice`, `validateUserInput`)
- **Variables**: Descriptive nouns (`userAccountBalance`, `authenticationToken`)
- **Booleans**: is/has/can prefixes (`isAuthenticated`, `hasPermission`)

### Function Design
- Each function does ONE thing
- 10-20 lines maximum when possible
- Use early returns and guard clauses
- Same level of abstraction within a function

```js
// BAD - mixed abstraction
async function processOrder(order) {
  const user = await database.query(`SELECT * FROM users WHERE id = ${order.userID}`);
  if (!user.isActive) throw new Error('Inactive user');
  await sendEmail(user.email, 'Order confirmed');
  return { success: true };
}

// GOOD - consistent abstraction
async function processOrder(order) {
  const user = await fetchUser(order.userID);
  validateUserIsActive(user);
  await notifyOrderConfirmation(user);
  return createSuccessResponse();
}
```

```js
// BAD - nested conditionals
function processUser(user) {
  if (user) {
    if (user.isActive) {
      if (user.hasPermission) {
        return doWork(user);
      }
    }
  }
  return null;
}

// GOOD - guard clauses
function processUser(user) {
  if (!user) return null;
  if (!user.isActive) return null;
  if (!user.hasPermission) return null;
  return doWork(user);
}
```

### Functional Style
- Prefer pure functions (same inputs → same outputs)
- Avoid side effects and mutations
- But readability wins over functional purity

```js
// GOOD - functional and readable
const activeUserEmails = users
  .filter(user => user.isActive)
  .map(user => user.email);

// Also GOOD - imperative but clear
const result = {};
for (const item of items) {
  if (item.isValid) {
    result[item.id] = item.value;
  }
}
```

### TypeScript Types
- Define meaningful domain types (User, Order, Product)
- Avoid: `any`, `as` assertions, non-null assertions (!)
- Use `unknown` at boundaries before validation, then narrow to a proper type
- Validate at boundaries, trust internal code

```ts
// BAD
function processData(data: any) {
  return data.map((item: any) => item.value);
}

// GOOD
function processData(data: unknown): string[] {
  const validatedData: DataItem[] = validateAndParseData(data);
  return validatedData.map(item => item.value);
}
```

## Error Handling

**Throw errors only for real errors:**
- External API failures
- Network errors
- Resource exhaustion (not enough credits, disk full)
- Authentication/authorization failures
- Database connection issues

**Be specific and accurate:**
```ts
// BAD - vague
throw new Error('Something went wrong');

// GOOD - specific
throw new Error('Stripe API returned 402: insufficient funds for charge');
```

**Don't wrap everything in try-catch:**
- Only catch errors you expect and can handle
- Let unexpected errors bubble up naturally
- Catching everything hides bugs

```ts
// BAD - catching everything
try {
  const user = await fetchUser(id);
  const orders = await fetchOrders(user.id);
  return processOrders(orders);
} catch (error) {
  return null; // Hides all problems
}

// GOOD - catch specific expected errors
const user = await fetchUser(id);
const orders = await fetchOrders(user.id);
return processOrders(orders);
// Let errors bubble up - they indicate real problems
```

**Handle edge cases without throwing:**
```ts
// BAD - throwing for non-errors
function findUser(users: User[], id: string): User {
  const user = users.find(u => u.id === id);
  if (!user) throw new Error('User not found');
  return user;
}

// GOOD - handle expected cases gracefully
function findUser(users: User[], id: string): User | undefined {
  return users.find(user => user.id === id);
}
```

**Validate at boundaries:**
- Validate user input at entry points
- Validate external API responses
- Trust internal code once validated

## Quality Checklist

Before considering implementation complete:
- [ ] Code is readable without comments
- [ ] Functions are small and focused
- [ ] No abbreviations in names
- [ ] No redundant operations
- [ ] No dead code
- [ ] Edge cases handled
- [ ] Follows existing codebase patterns
- [ ] Efficient - no unnecessary work
- [ ] Proper types defined (no any/unknown cheating)
