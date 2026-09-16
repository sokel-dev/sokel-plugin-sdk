# Rules whose failure mode is silence

Every rule here has the same shape: break it and **nothing reports an error**. That is what
separates them from advice. They come from plugins that shipped broken.

## Contract and implementation must line up, field by field

**A field that is not in the contract never reaches the implementation**, and nothing says so.
Adding an input to the handler without declaring it looks like "I passed it and nothing happened".

A real case: a chat contract missing `Tools` / `Reasoning` / `ResponseFormat`. Tool calling sent no
tools, thinking was never enabled — the features were entirely dead, without a word.

The reverse is just as bad: declaring a field the implementation never produces puts a permanently
empty variable on the canvas for people to reference.

## Bind with `contract.BindInput`, not `json.Unmarshal`

`BindInput` walks `sokel` tags recursively. `json.Unmarshal` only knows json tags and Go field
names, and Go's case-insensitive match does not bridge camelCase and snake_case.

A real case: the contract name `responseType` failed to land in `ResponseType`, so file mode
silently ran as text mode and produced a file with the right name and no bytes. Generated types
carry both `json` and `sokel` tags for this reason; **hand-written nested types need the `sokel`
tag too**.

## Optional numbers are pointers

```go
Temperature *float64 `sokel:"Temperature,optional"`
```

A value type turns "not set" into `0` and sends it upstream — silently changing model behaviour
with nothing to see.

## Empty strings still appear in typed outputs

Typed outputs flatten every field. Nil pointers and interfaces are dropped; empty strings are not.
"Nothing this time" should not arrive downstream as an empty value — split the operation or use a
pointer.

## Streaming: the delta and the accumulation are different fields

Platform semantics are "a later frame overwrites the same field". Put both on one field and the
consumer sees only the last fragment.

## Opaque fields must state a reason

`field.Any(name, reason)` / `field.Object(name, reason)` — the reason is a required argument; it
will not compile without one. "I could not be bothered" and "it genuinely has no structure" look
identical in a file, and the reason is the only thing that separates them. In practice most
"structureless" payloads do have a structure nobody wrote down.

## Event common fields are listed explicitly

They are never intersected automatically. If they were, one new event missing one field would
silently shrink the common set and break live workflows — and nobody would suspect this.

## Credentials hold identity; environment holds deployment

Would this value still be true for the same plugin on another machine? If not, it is environment,
not credential.

## An optional secret is injected only when it has a value

```go
// right
if key := strings.TrimSpace(cred.APIKey); key != "" {
    env = append(env, "ANTHROPIC_API_KEY="+key)
}

// wrong: when the user left it blank, this wipes a login already present on the machine
env = append(env, "ANTHROPIC_API_KEY="+cred.APIKey)
```

## Set the operation timeout

The platform default is 60s. Heavy work — transcription, long generation, an agent run — gets cut
off halfway, and the person dragging the node onto a canvas has no idea what to type. The plugin is
the only party that knows how long it takes.

## Error text is read by users

- Good: "missing access token (set it in the plugin's credentials, scope `api` or wider)"
- Useless: "unauthorized"

## Read big JSON line by line with a buffered reader, not a scanner

A line scanner's default limit is 64KB. Some upstreams open with a line well past that, and the
scanner then **stops silently** — no error, just no further lines, presenting as "it ran and
produced nothing".

## Single-operation plugins may be called without naming the operation

The platform is allowed to omit the operation field when a plugin has exactly one. Generated
registration handles it; hand-rolled dispatch must not assume the field is present.

---

# Verifying against a real upstream

The most expensive rule here, and the one most often skipped.

One real call to a real instance routinely exposes what no amount of reading finds. From a single
call to one service's events API:

| Assumed | Actually |
|---|---|
| the id field is the issue number | it is the comment's own id; the number is elsewhere in the payload |
| issue comments and merge-request comments are different events | one event, discriminated by a type field |
| every note was written by a person | label and assignee changes are notes too, flagged as system |

Each of those routes a trigger onto the wrong object or burns a downstream run.

**Fixtures must be payloads captured from the real service.** A fixture you wrote yourself grows to
match your understanding of the API — which is precisely why it will always be green.

**Assert on the bytes actually sent.** Record them with a fake upstream, especially once an official
SDK builds the request: your structs are then no longer the authority.

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    b, _ := io.ReadAll(r.Body)
    _ = json.Unmarshal(b, &captured)
    io.WriteString(w, respBody)
}))
```

Give the fake upstream a real `Content-Type`. A real API always returns one and official SDKs check
it, even where a hand-written client does not — so this class of bug tends to surface only when
someone switches to the SDK.

For event sources, use a record-only source context and assert "which input emits which event, and
whether the payload is flattened correctly".

**When a plugin drives an external CLI, check the flags against `--help` on the real version**, not
against its documentation, and pin them in a test so the next version tells you.

## Ways a test is green while the thing is broken

| Written like this | Why it is fake green |
|---|---|
| a fixture you invented | it grew to match your understanding |
| asserting on an AI's wording | that is not an assertion; assert on structure — was the right tool called, are the fields there |
| a fixture missing a whole class of input | the branch handling that class never runs |
| grepping the source for a keyword | changing the call back still greps fine |
| asserting a replacement "happened" without asserting it matched | a replacement that matched nothing also "succeeds" |

---

# Every plugin ships two documents

- `README.md` — for whoever changes the code: design decisions, upstream quirks, what was tried.
- `docs/<plugin>.md` — for whoever uses it: how to configure it, what it can do.

Both, always. They address different readers and neither substitutes for the other.
